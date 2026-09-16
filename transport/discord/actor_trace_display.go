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
	actorTraceFlushDelay   = 350 * time.Millisecond
	actorTraceMaxTextRunes = 3900
	actorTraceArgsRunes    = 120

	mainTraceAccent = 0x5865F2 // blurple — planner stream
	subTraceAccent  = 0x57F287 // green — worker stream
)

// actorTraceState is one Components V2 message stream per actor. Each state
// remembers which channel message was created last (msgSeq); when any newer
// message exists in the channel, the next appended line seals the old
// message and opens a fresh one below it, so cross-actor ordering always
// matches event time (REQ-031).
type actorTraceState struct {
	messageID string
	msgSeq    int64
	items     []string
	dirty     bool
	completed bool
	timer     *time.Timer
}

var actorTraceMu sync.Mutex
var actorTraceStates = make(map[string]*actorTraceState)
var actorTraceChannelSeq = make(map[string]int64)

func (g *Gateway) displayActorOutput(ctx context.Context, output sdk.Output) error {
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
		chKey := fmt.Sprintf("%p\x00%s", g, channelID)
		return g.sendActorResponse(ctx, channelID, chKey, actorFromMetadata(output.Metadata), output.Metadata["trace_job_id"], text)
	}
	actor := actorFromMetadata(output.Metadata)
	jobID := strings.TrimSpace(output.Metadata["trace_job_id"])
	chKey := fmt.Sprintf("%p\x00%s", g, channelID)
	key := chKey + "\x00" + actor + "\x00" + jobID
	return g.displayActorTrace(ctx, channelID, chKey, key, actor, jobID, *output.Trace)
}

func actorFromMetadata(metadata map[string]string) string {
	if strings.EqualFold(strings.TrimSpace(metadata["trace_actor"]), "subagent") {
		return "subagent"
	}
	return "main"
}

func workerJobLabel(jobID string) string {
	digits := strings.TrimPrefix(jobID, "sa-")
	if len(digits) > 6 {
		digits = digits[len(digits)-6:]
	}
	if jobID == "" {
		return "🛠 worker"
	}
	return "🛠 worker …" + digits
}

func actorLabel(actor, jobID string) string {
	if actor == "subagent" {
		return workerJobLabel(jobID)
	}
	return "🤖 Main Agent"
}

func actorAccent(actor string) int {
	if actor == "subagent" {
		return subTraceAccent
	}
	return mainTraceAccent
}

func eventElapsed(trace sdk.TraceEvent) time.Duration {
	if d := trace.TotalElapsed(); d > 0 {
		return d
	}
	return trace.Elapsed
}

func eventProviderLatency(trace sdk.TraceEvent) time.Duration {
	return trace.ProviderLatency()
}

func (g *Gateway) displayActorTrace(ctx context.Context, channelID, chKey, key, actor, jobID string, trace sdk.TraceEvent) error {
	switch trace.Stage {
	case sdk.TraceRequest:
		if actor == "main" {
			g.actorTraceBeginTurn(key)
		}
		return g.actorTraceAppend(ctx, channelID, chKey, key, "⏳ sending request to provider")
	case sdk.TraceProviderReady:
		return g.actorTraceUpdate(ctx, channelID, chKey, key, "⏳ provider accepted · "+formatDuration(eventProviderLatency(trace)), func(item string) bool {
			return strings.HasPrefix(item, "⏳ sending request to provider")
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
		// A container holding only the "provider accepted" line becomes the
		// response itself (edited in place); otherwise the pending marker is
		// dropped from the old container and the content follows as a new
		// message in creation order (REQ-037).
		pages := paginateActorText(text, actorTraceMaxTextRunes)
		actorTraceMu.Lock()
		state := actorTraceStates[key]
		if state != nil && state.messageID != "" && len(state.items) == 1 &&
			strings.HasPrefix(state.items[0], "⏳ provider accepted") {
			state.items = pages
			state.dirty = false
			messageID := state.messageID
			label := actorLabelFromKey(key)
			accent := accentFromKey(key)
			actorTraceMu.Unlock()
			return g.EditComponentsV2(ctx, channelID, messageID, actorTraceComponents(label, accent, pages))
		}
		if state != nil {
			kept := make([]string, 0, len(state.items))
			removed := false
			for _, item := range state.items {
				if strings.HasPrefix(item, "⏳ ") {
					removed = true
					continue
				}
				kept = append(kept, item)
			}
			if removed {
				state.items = kept
				if len(kept) == 0 && state.messageID != "" {
					// Nothing visible left: the flushed message stays with its
					// history; dropping the marker line needs an edit.
					state.messageID = ""
					state.items = nil
					state.dirty = false
				} else {
					state.dirty = true
				}
			}
		}
		actorTraceMu.Unlock()
		if err := g.actorTraceFlush(ctx, channelID, chKey, key); err != nil {
			return err
		}
		return g.sendActorResponse(ctx, channelID, chKey, actor, jobID, text)
	case sdk.TraceToolCall:
		if trace.ToolCall == nil {
			return nil
		}
		// The first tool of a response replaces that request's pending row:
		// "provider accepted" is merged into the tool line (REQ-033).
		if err := g.actorTraceUpdate(ctx, channelID, chKey, key, formatTraceToolLine(trace.ToolCall, "🔧", trace), func(item string) bool {
			return strings.HasPrefix(item, "⏳ ")
		}); err != nil {
			return err
		}
		return g.actorTraceFlush(ctx, channelID, chKey, key)
	case sdk.TraceToolRunning:
		return nil
	case sdk.TraceToolResult:
		if trace.ToolCall == nil || trace.ToolResult == nil {
			return nil
		}
		marker := "✅"
		if trace.ToolResult.IsError {
			marker = "❌"
		}
		return g.actorTraceUpdate(ctx, channelID, chKey, key, formatTraceToolLine(trace.ToolCall, marker, trace), func(item string) bool {
			return strings.HasPrefix(item, "🔧 "+trace.ToolCall.Name)
		})
	case sdk.TraceRetryWait:
		text := "↻ retrying request"
		if trace.Err != nil {
			text += " (" + safeErrorSummary(trace.Err) + ")"
		}
		if trace.RetryAfter > 0 {
			text += " in " + formatDuration(trace.RetryAfter)
		}
		return g.actorTraceAppend(ctx, channelID, chKey, key, text)
	case sdk.TraceResponse:
		g.actorTraceComplete(key)
		return g.actorTraceFlush(ctx, channelID, chKey, key)
	case sdk.TraceError:
		text := "❌ request failed"
		if trace.Err != nil {
			text += ": " + safeErrorSummary(trace.Err)
		}
		g.actorTraceComplete(key)
		if err := g.actorTraceAppend(ctx, channelID, chKey, key, text); err != nil {
			return err
		}
		return g.actorTraceFlush(ctx, channelID, chKey, key)
	default:
		return nil
	}
}

// formatTraceToolLine renders one tool line: status, name, arguments
// (one-line, capped) and, once timed, provider latency plus the
// request-sent → execution-complete total from subtracted timestamps
// (REQ-032, REQ-033).
func formatTraceToolLine(call *sdk.ToolCall, marker string, trace sdk.TraceEvent) string {
	line := marker + " " + truncateOneLine(call.Name, 80)
	if args := oneLine(call.Arguments); args != "" && args != "{}" {
		line += " · " + truncateRunes(args, actorTraceArgsRunes)
	}
	if d := eventProviderLatency(trace); d > 0 {
		line += " · provider " + formatDuration(d)
	}
	if d := eventElapsed(trace); d > 0 {
		line += " · total " + formatDuration(d)
	}
	return line
}

func truncateRunes(text string, max int) string {
	if max <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max-1]) + "…"
}

func (g *Gateway) actorTraceBeginTurn(key string) {
	actorTraceMu.Lock()
	defer actorTraceMu.Unlock()
	state := actorTraceStates[key]
	if state != nil && state.completed {
		if state.timer != nil {
			state.timer.Stop()
		}
		delete(actorTraceStates, key)
	}
}

func (g *Gateway) actorTraceComplete(key string) {
	actorTraceMu.Lock()
	defer actorTraceMu.Unlock()
	if state := actorTraceStates[key]; state != nil {
		state.completed = true
	}
}

func (g *Gateway) actorTraceAppend(ctx context.Context, channelID, chKey, key, item string) error {
	actorTraceMu.Lock()
	g.sealStaleStateLocked(ctx, channelID, chKey, key)
	state := actorTraceStateForKeyLocked(key)
	state.items = append(state.items, item)
	trimActorTraceItems(state)
	state.dirty = true
	g.scheduleActorTraceFlushLocked(channelID, chKey, key, state)
	actorTraceMu.Unlock()
	return nil
}

func (g *Gateway) actorTraceUpdate(ctx context.Context, channelID, chKey, key, item string, match func(string) bool) error {
	actorTraceMu.Lock()
	// A stale message must never receive an in-place edit: the result line
	// belongs below every newer message instead (REQ-031).
	g.sealStaleStateLocked(ctx, channelID, chKey, key)
	state := actorTraceStates[key]
	if state != nil {
		for i := len(state.items) - 1; i >= 0; i-- {
			if match != nil && match(state.items[i]) {
				state.items[i] = item
				state.dirty = true
				g.scheduleActorTraceFlushLocked(channelID, chKey, key, state)
				actorTraceMu.Unlock()
				return nil
			}
		}
	}
	state = actorTraceStateForKeyLocked(key)
	state.items = append(state.items, item)
	trimActorTraceItems(state)
	state.dirty = true
	g.scheduleActorTraceFlushLocked(channelID, chKey, key, state)
	actorTraceMu.Unlock()
	return nil
}

// sealStaleStateLocked closes the actor's current message when any newer
// message exists in the channel, so the next flush starts a segment below it.
// The caller must hold actorTraceMu.
func (g *Gateway) sealStaleStateLocked(_ context.Context, _, chKey, key string) {
	state := actorTraceStates[key]
	if state == nil || state.messageID == "" {
		return
	}
	if actorTraceChannelSeq[chKey] > state.msgSeq {
		if state.timer != nil {
			state.timer.Stop()
			state.timer = nil
		}
		state.messageID = ""
		state.items = nil
		state.dirty = true
	}
}

func (g *Gateway) actorTraceFlush(ctx context.Context, channelID, chKey, key string) error {
	actorTraceMu.Lock()
	state := actorTraceStateForKeyLocked(key)
	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
	if !state.dirty || len(state.items) == 0 {
		actorTraceMu.Unlock()
		return nil
	}
	items := append([]string(nil), state.items...)
	messageID := state.messageID
	label := actorLabelFromKey(key)
	accent := accentFromKey(key)
	actorTraceMu.Unlock()
	components := actorTraceComponents(label, accent, items)
	if messageID != "" {
		if err := g.EditComponentsV2(ctx, channelID, messageID, components); err == nil {
			actorTraceMu.Lock()
			if state := actorTraceStates[key]; state != nil {
				state.dirty = false
			}
			actorTraceMu.Unlock()
			return nil
		} else if !isUnknownMessage(err) {
			return err
		}
	}
	actorTraceMu.Lock()
	actorTraceChannelSeq[chKey]++
	seq := actorTraceChannelSeq[chKey]
	actorTraceMu.Unlock()
	id, err := g.SendComponentsV2(ctx, channelID, components)
	if err != nil {
		return err
	}
	actorTraceMu.Lock()
	if state := actorTraceStates[key]; state != nil {
		state.messageID = id
		state.msgSeq = seq
		state.dirty = false
	}
	actorTraceMu.Unlock()
	return nil
}

func actorLabelFromKey(key string) string {
	parts := strings.Split(key, "\x00")
	if len(parts) < 4 {
		return "🤖 Main Agent"
	}
	return actorLabel(parts[2], parts[3])
}

func accentFromKey(key string) int {
	parts := strings.Split(key, "\x00")
	if len(parts) >= 3 && parts[2] == "subagent" {
		return subTraceAccent
	}
	return mainTraceAccent
}

func actorTraceStateForKeyLocked(key string) *actorTraceState {
	state := actorTraceStates[key]
	if state == nil {
		state = &actorTraceState{}
		actorTraceStates[key] = state
	}
	return state
}

func (g *Gateway) scheduleActorTraceFlushLocked(channelID, chKey, key string, state *actorTraceState) {
	if state.timer != nil {
		return
	}
	state.timer = time.AfterFunc(actorTraceFlushDelay, func() {
		_ = g.actorTraceFlush(context.Background(), channelID, chKey, key)
	})
}

func trimActorTraceItems(state *actorTraceState) {
	for len(state.items) > 1 && actorTraceSize(state.items) > actorTraceMaxTextRunes {
		state.items = state.items[1:]
	}
}

func actorTraceSize(items []string) int {
	total := 0
	for i, item := range items {
		if i > 0 {
			total++
		}
		total += len([]rune(item))
	}
	return total
}

// actorTraceComponents renders one actor's stream as a Components V2
// Container: header line plus TextDisplay pages, with the actor's accent
// color (REQ-022, REQ-031).
func actorTraceComponents(label string, accent int, items []string) []discordgo.MessageComponent {
	texts := []string{"**" + label + "**"}
	texts = append(texts, chunkTraceItems(items)...)
	children := make([]discordgo.MessageComponent, 0, len(texts))
	for _, text := range texts {
		children = append(children, discordgo.TextDisplay{Content: text})
	}
	color := accent
	return []discordgo.MessageComponent{discordgo.Container{AccentColor: &color, Components: children}}
}

func chunkTraceItems(items []string) []string {
	var pages []string
	current := strings.Builder{}
	for _, item := range items {
		if current.Len() > 0 && len([]rune(current.String()))+1+len([]rune(item)) > actorTraceMaxTextRunes {
			pages = append(pages, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteByte('\n')
		}
		current.WriteString(item)
	}
	if current.Len() > 0 {
		pages = append(pages, current.String())
	}
	if len(pages) == 0 {
		pages = []string{""}
	}
	for len(pages) > 9 {
		pages = pages[len(pages)-9:]
	}
	return pages
}

func (g *Gateway) sendActorResponse(ctx context.Context, channelID, chKey, actor, jobID, text string) error {
	label := actorLabel(actor, jobID)
	accent := actorAccent(actor)
	for _, page := range paginateActorText(text, actorTraceMaxTextRunes) {
		actorTraceMu.Lock()
		actorTraceChannelSeq[chKey]++
		actorTraceMu.Unlock()
		color := accent
		container := discordgo.Container{AccentColor: &color, Components: []discordgo.MessageComponent{discordgo.TextDisplay{Content: label + "\n\n" + page}}}
		if _, err := g.SendComponentsV2(ctx, channelID, []discordgo.MessageComponent{container}); err != nil {
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
