package discord

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tulipskun/ai/sdk"
	"github.com/bwmarrin/discordgo"
)

const (
	mainAgentEmbedColor    = 0x5865F2
	subAgentEmbedColor     = 0x57F287
	actorTraceFlushDelay   = 350 * time.Millisecond
	actorTraceMaxEmbedSize = 4000
)

type actorTraceState struct {
	messageID string
	items     []string
	dirty     bool
	completed bool
	timer     *time.Timer
}

var actorTraceMu sync.Mutex
var actorTraceStates = make(map[string]*actorTraceState)

func (g *Gateway) displayActorOutput(ctx context.Context, output sdk.Output) error {
	if g == nil { return errors.New("discord: gateway is not initialized") }
	channelID := strings.TrimSpace(output.Metadata["channel_id"])
	if channelID == "" { return errors.New("discord: output has no channel_id") }
	if output.Trace == nil {
		text := responseContent(&output.Response)
		if text == "" { text = responseContent(&sdk.Response{Content: output.Content}) }
		return g.sendActorResponse(ctx, channelID, actorFromMetadata(output.Metadata), output.Metadata["trace_job_id"], text)
	}
	actor := actorFromMetadata(output.Metadata)
	jobID := strings.TrimSpace(output.Metadata["trace_job_id"])
	key := fmt.Sprintf("%p\x00%s\x00%s\x00%s", g, channelID, actor, jobID)
	return g.displayActorTrace(ctx, channelID, key, actor, jobID, *output.Trace)
}

func actorFromMetadata(metadata map[string]string) string {
	if strings.EqualFold(strings.TrimSpace(metadata["trace_actor"]), "subagent") { return "subagent" }
	return "main"
}

func actorTraceLabel(actor, jobID string) string {
	if actor == "subagent" {
		if jobID != "" { return "Sub-agent " + jobID }
		return "Sub-agent"
	}
	return "Main Agent"
}

func actorTraceColor(actor string) int {
	if actor == "subagent" { return subAgentEmbedColor }
	return mainAgentEmbedColor
}

func (g *Gateway) displayActorTrace(ctx context.Context, channelID, key, actor, jobID string, trace sdk.TraceEvent) error {
	switch trace.Stage {
	case sdk.TraceRequest:
		g.actorTraceBeginTurn(key)
		return g.actorTraceAppend(ctx, channelID, key, actor, jobID, "sending request to provider", true)
	case sdk.TraceProviderReady:
		return g.actorTraceUpdate(ctx, channelID, key, actor, jobID, withElapsed("provider accepted request; processing", trace.Elapsed), func(item string) bool {
			return strings.HasPrefix(item, "sending request to provider")
		})
	case sdk.TraceResponseText:
		return nil
	case sdk.TraceResponseContent:
		text := strings.TrimSpace(trace.Text)
		if text == "" { text = responseContent(trace.Response) }
		if text == "" { return nil }
		return g.sendActorResponse(ctx, channelID, actor, jobID, text)
	case sdk.TraceToolCall:
		if trace.ToolCall == nil { return nil }
		if err := g.actorTraceAppend(ctx, channelID, key, actor, jobID, withElapsed(formatActorToolCall(trace.ToolCall), trace.Elapsed), false); err != nil { return err }
		return g.actorTraceFlush(ctx, channelID, key, actor, jobID)
	case sdk.TraceToolRunning:
		return nil
	case sdk.TraceToolResult:
		if trace.ToolCall == nil || trace.ToolResult == nil { return nil }
		return g.actorTraceUpdate(ctx, channelID, key, actor, jobID, withElapsed(formatActorToolResult(trace.ToolCall, trace.ToolResult), trace.Elapsed), func(item string) bool {
			return strings.HasPrefix(item, formatActorToolCall(trace.ToolCall))
		})
	case sdk.TraceRetryWait:
		text := "Retrying request"
		if trace.RetryAfter > 0 { text += " in " + formatDuration(trace.RetryAfter) }
		return g.actorTraceAppend(ctx, channelID, key, actor, jobID, text, false)
	case sdk.TraceResponse:
		g.actorTraceComplete(key)
		return g.actorTraceFlush(ctx, channelID, key, actor, jobID)
	case sdk.TraceError:
		text := "❌ Request failed"
		if trace.Err != nil { text += ": " + safeErrorSummary(trace.Err) }
		if err := g.actorTraceAppend(ctx, channelID, key, actor, jobID, text, false); err != nil { return err }
		g.actorTraceComplete(key)
		return g.actorTraceFlush(ctx, channelID, key, actor, jobID)
	default:
		return nil
	}
}

func formatActorToolCall(call *sdk.ToolCall) string {
	if call == nil { return "tool" }
	name := strings.TrimSpace(call.Name)
	if name == "" { return "tool" }
	return truncateOneLine("tool: "+name, maxToolTraceLength)
}

func formatActorToolResult(call *sdk.ToolCall, result *sdk.ToolResult) string {
	if result == nil { return "" }
	prefix := "✅ "
	if result.IsError { prefix = "❌ " }
	return prefix + formatActorToolCall(call)
}

func (g *Gateway) actorTraceBeginTurn(key string) {
	actorTraceMu.Lock()
	defer actorTraceMu.Unlock()
	state := actorTraceStates[key]
	if state != nil && state.completed {
		if state.timer != nil { state.timer.Stop() }
		delete(actorTraceStates, key)
		state = nil
	}
	if state == nil {
		state = &actorTraceState{}
		actorTraceStates[key] = state
	}
	state.completed = false
}

func (g *Gateway) actorTraceComplete(key string) {
	actorTraceMu.Lock()
	defer actorTraceMu.Unlock()
	if state := actorTraceStates[key]; state != nil { state.completed = true }
}

func (g *Gateway) actorTraceAppend(ctx context.Context, channelID, key, actor, jobID, item string, replacePending bool) error {
	actorTraceMu.Lock()
	state := actorTraceStateForKeyLocked(key)
	if replacePending {
		for i := len(state.items) - 1; i >= 0; i-- {
			if state.items[i] == "sending request to provider" {
				state.items[i] = item
				state.dirty = true
				g.scheduleActorTraceFlushLocked(channelID, key, actor, jobID, state)
				actorTraceMu.Unlock()
				return nil
			}
		}
	}
	state.items = append(state.items, item)
	trimActorTraceItems(state)
	state.dirty = true
	g.scheduleActorTraceFlushLocked(channelID, key, actor, jobID, state)
	actorTraceMu.Unlock()
	return nil
}

func (g *Gateway) actorTraceUpdate(ctx context.Context, channelID, key, actor, jobID, item string, match func(string) bool) error {
	actorTraceMu.Lock()
	state := actorTraceStateForKeyLocked(key)
	for i := len(state.items) - 1; i >= 0; i-- {
		if match != nil && match(state.items[i]) {
			state.items[i] = item
			state.dirty = true
			g.scheduleActorTraceFlushLocked(channelID, key, actor, jobID, state)
			actorTraceMu.Unlock()
			return nil
		}
	}
	state.items = append(state.items, item)
	trimActorTraceItems(state)
	state.dirty = true
	g.scheduleActorTraceFlushLocked(channelID, key, actor, jobID, state)
	actorTraceMu.Unlock()
	return nil
}

func (g *Gateway) actorTraceFlush(ctx context.Context, channelID, key, actor, jobID string) error {
	actorTraceMu.Lock()
	state := actorTraceStateForKeyLocked(key)
	if state.timer != nil { state.timer.Stop(); state.timer = nil }
	if !state.dirty || len(state.items) == 0 { actorTraceMu.Unlock(); return nil }
	items := append([]string(nil), state.items...)
	messageID := state.messageID
	actorTraceMu.Unlock()
	embed := actorTraceEmbed(actor, jobID, items)
	if messageID != "" {
		if err := g.EditEmbed(ctx, channelID, messageID, embed); err == nil {
			actorTraceMu.Lock()
			if state := actorTraceStates[key]; state != nil { state.dirty = false }
			actorTraceMu.Unlock()
			return nil
		} else if !isUnknownMessage(err) { return err }
	}
	id, err := g.SendEmbed(ctx, channelID, embed)
	if err != nil { return err }
	actorTraceMu.Lock()
	if state := actorTraceStates[key]; state != nil { state.messageID = id; state.dirty = false }
	actorTraceMu.Unlock()
	return nil
}

func (g *Gateway) actorTraceReset(key string) {
	actorTraceMu.Lock()
	defer actorTraceMu.Unlock()
	if state := actorTraceStates[key]; state != nil && state.timer != nil { state.timer.Stop() }
	delete(actorTraceStates, key)
}

func actorTraceStateForKeyLocked(key string) *actorTraceState {
	state := actorTraceStates[key]
	if state == nil {
		state = &actorTraceState{}
		actorTraceStates[key] = state
	}
	return state
}

func (g *Gateway) scheduleActorTraceFlushLocked(channelID, key, actor, jobID string, state *actorTraceState) {
	if state.timer != nil { return }
	state.timer = time.AfterFunc(actorTraceFlushDelay, func() { _ = g.actorTraceFlush(context.Background(), channelID, key, actor, jobID) })
}

func trimActorTraceItems(state *actorTraceState) {
	for len(state.items) > 1 && actorTraceDescriptionSize(state.items) > actorTraceMaxEmbedSize { state.items = state.items[1:] }
}

func actorTraceDescriptionSize(items []string) int {
	total := 0
	for i, item := range items {
		if i > 0 { total++ }
		total += len([]rune(item))
	}
	return total
}

func actorTraceEmbed(actor, jobID string, items []string) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{Description: strings.Join(items, "\n"), Color: actorTraceColor(actor)}
	embed.Author = &discordgo.MessageEmbedAuthor{Name: actorTraceLabel(actor, jobID)}
	return embed
}

func (g *Gateway) sendActorResponse(ctx context.Context, channelID, actor, jobID, text string) error {
	for _, page := range paginateActorText(text, actorTraceMaxEmbedSize) {
		if _, err := g.SendEmbed(ctx, channelID, actorTraceEmbed(actor, jobID, []string{page})); err != nil { return err }
	}
	return nil
}

func paginateActorText(text string, max int) []string {
	text = strings.TrimSpace(text)
	if text == "" { return nil }
	if max <= 0 { return []string{text} }
	var pages []string
	for len([]rune(text)) > max {
		runes := []rune(text)
		cut := max
		for i := max; i > 0; i-- {
			if runes[i-1] == '\n' || runes[i-1] == ' ' { cut = i; break }
		}
		page := strings.TrimSpace(string(runes[:cut]))
		if page == "" { page = string(runes[:max]) }
		pages = append(pages, page)
		text = strings.TrimSpace(string(runes[cut:]))
	}
	if text != "" { pages = append(pages, text) }
	return pages
}
