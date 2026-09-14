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
	traceFlushDelay        = 350 * time.Millisecond
	actorTraceMaxEmbedSize = 4000
)

type actorTraceState struct {
	messageID string
	items     []string
	dirty     bool
	timer     *time.Timer
}

func (g *Gateway) Display(ctx context.Context, output sdk.Output) error {
	if g == nil {
		return errors.New("discord: gateway is not initialized")
	}
	channelID := strings.TrimSpace(output.Metadata["channel_id"])
	if channelID == "" {
		return errors.New("discord: output has no channel_id")
	}
	if output.Trace == nil {
		text := responseContent(&output.Response)
		if text == "" {
			text = responseContent(&sdk.Response{Content: output.Content})
		}
		return g.sendActorResponse(ctx, channelID, actorFromMetadata(output.Metadata), output.Metadata["trace_job_id"], text)
	}
	actor := actorFromMetadata(output.Metadata)
	jobID := strings.TrimSpace(output.Metadata["trace_job_id"])
	key := actor + "\x00" + jobID + "\x00" + channelID
	return g.displayActorTrace(ctx, channelID, key, actor, jobID, *output.Trace)
}

func actorFromMetadata(metadata map[string]string) string {
	if strings.EqualFold(strings.TrimSpace(metadata["trace_actor"]), "subagent") {
		return "subagent"
	}
	return "main"
}

func actorTraceLabel(actor, jobID string) string {
	if actor == "subagent" {
		if jobID != "" {
			return "Sub-agent " + jobID
		}
		return "Sub-agent"
	}
	return "Main Agent"
}

func actorTraceColor(actor string) int {
	if actor == "subagent" {
		return subAgentEmbedColor
	}
	return mainAgentEmbedColor
}

func (g *Gateway) displayActorTrace(ctx context.Context, channelID, key, actor, jobID string, trace sdk.TraceEvent) error {
	label := actorTraceLabel(actor, jobID)
	switch trace.Stage {
	case sdk.TraceRequest:
		g.actorTraceReset(key)
		return g.actorTraceAppend(ctx, channelID, key, actor, jobID, "sending request to provider", true)
	case sdk.TraceProviderReady:
		return g.actorTraceUpdate(ctx, channelID, key, actor, jobID, withElapsed("provider accepted request; processing", trace.Elapsed), func(item string) bool {
			return strings.HasPrefix(item, "sending request to provider")
		})
	case sdk.TraceResponseText:
		return nil
	case sdk.TraceResponseContent:
		text := strings.TrimSpace(trace.Text)
		if text == "" {
			text = responseContent(trace.Response)
		}
		if text == "" {
			return nil
		}
		return g.sendActorResponse(ctx, channelID, actor, jobID, text)
	case sdk.TraceToolCall:
		if trace.ToolCall == nil {
			return nil
		}
		return g.actorTraceAppend(ctx, channelID, key, actor, jobID, withElapsed(formatActorToolCall(trace.ToolCall), trace.Elapsed), false)
	case sdk.TraceToolRunning:
		return nil
	case sdk.TraceToolResult:
		if trace.ToolCall == nil || trace.ToolResult == nil {
			return nil
		}
		return g.actorTraceUpdate(ctx, channelID, key, actor, jobID, withElapsed(formatActorToolResult(trace.ToolCall, trace.ToolResult), trace.Elapsed), func(item string) bool {
			return strings.HasPrefix(item, formatActorToolCall(trace.ToolCall))
		})
	case sdk.TraceRetryWait:
		text := "Retrying request"
		if trace.RetryAfter > 0 {
			text += " in " + formatDuration(trace.RetryAfter)
		}
		return g.actorTraceAppend(ctx, channelID, key, actor, jobID, text, false)
	case sdk.TraceResponse:
		return g.actorTraceFlush(ctx, channelID, key, actor, jobID)
	case sdk.TraceError:
		text := "❌ Request failed"
		if trace.Err != nil {
			text += ": " + safeErrorSummary(trace.Err)
		}
		if err := g.actorTraceAppend(ctx, channelID, key, actor, jobID, text, false); err != nil {
			return err
		}
		return g.actorTraceFlush(ctx, channelID, key, actor, jobID)
	default:
		_ = label
		return nil
	}
}

func formatActorToolCall(call *sdk.ToolCall) string {
	if call == nil {
		return "tool()"
	}
	args := strings.TrimSpace(call.Arguments)
	if args == "" {
		return call.Name + "()"
	}
	return truncateOneLine(fmt.Sprintf("%s(%s)", call.Name, args), maxToolTraceLength)
}

func formatActorToolResult(call *sdk.ToolCall, result *sdk.ToolResult) string {
	if result == nil {
		return ""
	}
	prefix := "✅ "
	if result.IsError {
		prefix = "❌ "
	}
	return prefix + formatActorToolCall(call)
}

func (g *Gateway) actorTraceAppend(ctx context.Context, channelID, key, actor, jobID, item string, replacePending bool) error {
	g.toolTraceMu.Lock()
	defer g.toolTraceMu.Unlock()
	state := g.actorTraceState(key)
	if replacePending {
		for i := len(state.items) - 1; i >= 0; i-- {
			if state.items[i] == "sending request to provider" {
				state.items[i] = item
				state.dirty = true
				g.scheduleActorTraceFlushLocked(channelID, key, actor, jobID, state)
				return nil
			}
		}
	}
	state.items = append(state.items, item)
	trimActorTraceItems(state)
	state.dirty = true
	g.scheduleActorTraceFlushLocked(channelID, key, actor, jobID, state)
	return nil
}

func (g *Gateway) actorTraceUpdate(ctx context.Context, channelID, key, actor, jobID, item string, match func(string) bool) error {
	g.toolTraceMu.Lock()
	defer g.toolTraceMu.Unlock()
	state := g.actorTraceState(key)
	for i := len(state.items) - 1; i >= 0; i-- {
		if match != nil && match(state.items[i]) {
			state.items[i] = item
			state.dirty = true
			g.scheduleActorTraceFlushLocked(channelID, key, actor, jobID, state)
			return nil
		}
	}
	state.items = append(state.items, item)
	trimActorTraceItems(state)
	state.dirty = true
	g.scheduleActorTraceFlushLocked(channelID, key, actor, jobID, state)
	return nil
}

func (g *Gateway) actorTraceFlush(ctx context.Context, channelID, key, actor, jobID string) error {
	g.toolTraceMu.Lock()
	defer g.toolTraceMu.Unlock()
	state := g.actorTraceState(key)
	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
	if !state.dirty || len(state.items) == 0 {
		return nil
	}
	embed := actorTraceEmbed(actor, jobID, state.items)
	if state.messageID != "" {
		if err := g.EditEmbed(ctx, channelID, state.messageID, embed); err == nil {
			state.dirty = false
			return nil
		} else if !isUnknownMessage(err) {
			return err
		}
		state.messageID = ""
	}
	id, err := g.SendEmbed(ctx, channelID, embed)
	if err != nil {
		return err
	}
	state.messageID = id
	state.dirty = false
	return nil
}

func (g *Gateway) actorTraceReset(key string) {
	g.toolTraceMu.Lock()
	defer g.toolTraceMu.Unlock()
	if g.toolTrace != nil {
		if state := g.toolTrace[key]; state != nil && state.timer != nil {
			state.timer.Stop()
		}
		delete(g.toolTrace, key)
	}
}

func (g *Gateway) actorTraceState(key string) *actorTraceState {
	state, ok := g.toolTrace[key]
	if ok {
		return (*actorTraceState)(state)
	}
	state := &actorTraceState{}
	return g.storeActorTraceState(key, state)
}

func (g *Gateway) storeActorTraceState(key string, state *actorTraceState) *actorTraceState {
	if g.toolTrace == nil {
		g.toolTrace = make(map[string]*toolTraceState)
	}
	converted := &toolTraceState{messageID: state.messageID, items: append([]string(nil), state.items...), dirty: state.dirty}
	g.toolTrace[key] = converted
	return state
}

func (g *Gateway) scheduleActorTraceFlushLocked(channelID, key, actor, jobID string, state *actorTraceState) {
	if state.timer != nil {
		return
	}
	state.timer = time.AfterFunc(traceFlushDelay, func() {
		_ = g.actorTraceFlush(context.Background(), channelID, key, actor, jobID)
	})
}

func trimActorTraceItems(state *actorTraceState) {
	for len(state.items) > 1 && actorTraceDescriptionSize(state.items) > actorTraceMaxEmbedSize {
		state.items = state.items[1:]
	}
}

func actorTraceDescriptionSize(items []string) int {
	total := 0
	for i, item := range items {
		if i > 0 {
			total++
		}
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
	pages := paginateActorText(text, actorTraceMaxEmbedSize)
	for _, page := range pages {
		if _, err := g.SendEmbed(ctx, channelID, actorTraceEmbed(actor, jobID, []string{page})); err != nil {
			return err
		}
	}
	return nil
}

func paginateActorText(text string, max int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if max <= 0 {
		return []string{text}
	}
	var pages []string
	for len([]rune(text)) > max {
		runes := []rune(text)
		cut := max
		for i := max; i > 0; i-- {
			if runes[i-1] == '\n' || runes[i-1] == ' ' {
				cut = i
				break
			}
		}
		page := strings.TrimSpace(string(runes[:cut]))
		if page == "" {
			page = string(runes[:max])
		}
		pages = append(pages, page)
		text = strings.TrimSpace(string(runes[cut:]))
	}
	if text != "" {
		pages = append(pages, text)
	}
	return pages
}
