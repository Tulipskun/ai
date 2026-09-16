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
	actorTraceFlushDelay    = 350 * time.Millisecond
	actorTraceMaxTextRunes  = 3900
	actorTraceAccentBlurple = 0x5865F2
)

// actorTraceState is one chronological V2 progress stream per channel and
// gateway. Planner and worker lines share the stream in event order so the
// channel reads top-to-bottom as the turn actually happened (REQ-031).
type actorTraceState struct {
	messageID   string
	items       []string
	dirty       bool
	completed   bool
	timer       *time.Timer
	workerLabel map[string]bool
}

var actorTraceMu sync.Mutex
var actorTraceStates = make(map[string]*actorTraceState)

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
		return g.sendActorResponse(ctx, channelID, actorFromMetadata(output.Metadata), output.Metadata["trace_job_id"], text)
	}
	actor := actorFromMetadata(output.Metadata)
	jobID := strings.TrimSpace(output.Metadata["trace_job_id"])
	key := fmt.Sprintf("%p\x00%s", g, channelID)
	return g.displayActorTrace(ctx, channelID, key, actor, jobID, *output.Trace)
}

func actorFromMetadata(metadata map[string]string) string {
	if strings.EqualFold(strings.TrimSpace(metadata["trace_actor"]), "subagent") {
		return "subagent"
	}
	return "main"
}

func actorMarker(actor string) string {
	if actor == "subagent" {
		return "🛠"
	}
	return "🤖"
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

func eventElapsed(trace sdk.TraceEvent) time.Duration {
	if d := trace.TotalElapsed(); d > 0 {
		return d
	}
	return trace.Elapsed
}

func eventProviderLatency(trace sdk.TraceEvent) time.Duration {
	if d := trace.ProviderLatency(); d > 0 {
		return d
	}
	if trace.ProviderAcceptedMs > 0 && trace.RequestStartedMs > 0 {
		return trace.ProviderLatency()
	}
	return 0
}

func (g *Gateway) displayActorTrace(ctx context.Context, channelID, key, actor, jobID string, trace sdk.TraceEvent) error {
	marker := actorMarker(actor)
	if actor == "subagent" {
		marker = ""
		if err := g.actorTraceWorkerHeader(ctx, channelID, key, jobID); err != nil {
			return err
		}
		marker = workerJobLabel(jobID)
	}
	switch trace.Stage {
	case sdk.TraceRequest:
		if actor != "subagent" {
			g.actorTraceBeginTurn(key)
		}
		return g.actorTraceAppend(ctx, channelID, key, marker+" ⏳ sending request to provider", true)
	case sdk.TraceProviderReady:
		return g.actorTraceUpdate(ctx, channelID, key, marker+" ⏳ provider accepted · "+formatDuration(eventProviderLatency(trace)), func(item string) bool {
			return strings.HasPrefix(item, marker+" ⏳ sending request to provider")
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
		label := marker + " 🔧 " + truncateOneLine("tool: "+trace.ToolCall.Name, maxToolTraceLength)
		if d := eventProviderLatency(trace); d > 0 {
			label += " · provider " + formatDuration(d)
		}
		if err := g.actorTraceUpdate(ctx, channelID, key, label, func(item string) bool {
			return strings.HasPrefix(item, marker+" ⏳ ")
		}); err != nil {
			return err
		}
		return g.actorTraceFlush(ctx, channelID, key)
	case sdk.TraceToolRunning:
		return nil
	case sdk.TraceToolResult:
		if trace.ToolCall == nil || trace.ToolResult == nil {
			return nil
		}
		status := "✅"
		if trace.ToolResult.IsError {
			status = "❌"
		}
		label := marker + " " + status + " " + truncateOneLine("tool: "+trace.ToolCall.Name, maxToolTraceLength)
		if d := eventProviderLatency(trace); d > 0 {
			label += " · provider " + formatDuration(d)
		}
		if d := eventElapsed(trace); d > 0 {
			label += " · total " + formatDuration(d)
		}
		return g.actorTraceUpdate(ctx, channelID, key, label, func(item string) bool {
			return strings.HasPrefix(item, marker+" 🔧 tool: "+trace.ToolCall.Name)
		})
	case sdk.TraceRetryWait:
		text := marker + " ↻ retrying request"
		if trace.RetryAfter > 0 {
			text += " in " + formatDuration(trace.RetryAfter)
		}
		return g.actorTraceAppend(ctx, channelID, key, text, false)
	case sdk.TraceResponse:
		if actor == "subagent" {
			return nil
		}
		g.actorTraceComplete(key)
		return g.actorTraceFlush(ctx, channelID, key)
	case sdk.TraceError:
		text := marker + " ❌ request failed"
		if trace.Err != nil {
			text += ": " + safeErrorSummary(trace.Err)
		}
		if err := g.actorTraceAppend(ctx, channelID, key, text, false); err != nil {
			return err
		}
		if actor == "subagent" {
			return nil
		}
		g.actorTraceComplete(key)
		return g.actorTraceFlush(ctx, channelID, key)
	default:
		return nil
	}
}

func (g *Gateway) actorTraceWorkerHeader(ctx context.Context, channelID, key, jobID string) error {
	label := workerJobLabel(jobID)
	actorTraceMu.Lock()
	state := actorTraceStateForKeyLocked(key)
	if state.workerLabel == nil {
		state.workerLabel = map[string]bool{}
	}
	if state.workerLabel[label] {
		actorTraceMu.Unlock()
		return nil
	}
	state.workerLabel[label] = true
	actorTraceMu.Unlock()
	return g.actorTraceAppend(ctx, channelID, key, label, false)
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
	if state := actorTraceStates[key]; state != nil {
		state.completed = true
	}
}

func (g *Gateway) actorTraceAppend(ctx context.Context, channelID, key, item string, replacePending bool) error {
	actorTraceMu.Lock()
	state := actorTraceStateForKeyLocked(key)
	if replacePending {
		prefix := strings.TrimSuffix(item, " ⏳ sending request to provider") + " ⏳ "
		for i := len(state.items) - 1; i >= 0; i-- {
			if strings.HasPrefix(state.items[i], prefix) {
				state.items[i] = item
				state.dirty = true
				g.scheduleActorTraceFlushLocked(channelID, key, state)
				actorTraceMu.Unlock()
				return nil
			}
		}
	}
	state.items = append(state.items, item)
	trimActorTraceItems(state)
	state.dirty = true
	g.scheduleActorTraceFlushLocked(channelID, key, state)
	actorTraceMu.Unlock()
	return nil
}

func (g *Gateway) actorTraceUpdate(ctx context.Context, channelID, key, item string, match func(string) bool) error {
	actorTraceMu.Lock()
	state := actorTraceStateForKeyLocked(key)
	for i := len(state.items) - 1; i >= 0; i-- {
		if match != nil && match(state.items[i]) {
			state.items[i] = item
			state.dirty = true
			g.scheduleActorTraceFlushLocked(channelID, key, state)
			actorTraceMu.Unlock()
			return nil
		}
	}
	state.items = append(state.items, item)
	trimActorTraceItems(state)
	state.dirty = true
	g.scheduleActorTraceFlushLocked(channelID, key, state)
	actorTraceMu.Unlock()
	return nil
}

func (g *Gateway) actorTraceFlush(ctx context.Context, channelID, key string) error {
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
	actorTraceMu.Unlock()
	components := actorTraceComponents(items)
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
	id, err := g.SendComponentsV2(ctx, channelID, components)
	if err != nil {
		return err
	}
	actorTraceMu.Lock()
	if state := actorTraceStates[key]; state != nil {
		state.messageID = id
		state.dirty = false
	}
	actorTraceMu.Unlock()
	return nil
}

func (g *Gateway) actorTraceReset(key string) {
	actorTraceMu.Lock()
	defer actorTraceMu.Unlock()
	if state := actorTraceStates[key]; state != nil && state.timer != nil {
		state.timer.Stop()
	}
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

func (g *Gateway) scheduleActorTraceFlushLocked(channelID, key string, state *actorTraceState) {
	if state.timer != nil {
		return
	}
	state.timer = time.AfterFunc(actorTraceFlushDelay, func() {
		_ = g.actorTraceFlush(context.Background(), channelID, key)
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

// actorTraceComponents renders the trace stream as one Components V2
// Container (REQ-022, REQ-031): a header line plus one TextDisplay page per
// chunk of the chronological items.
func actorTraceComponents(items []string) []discordgo.MessageComponent {
	texts := []string{"**AI progress**"}
	texts = append(texts, chunkTraceItems(items)...)
	children := make([]discordgo.MessageComponent, 0, len(texts))
	for _, text := range texts {
		children = append(children, discordgo.TextDisplay{Content: text})
	}
	accent := actorTraceAccentBlurple
	return []discordgo.MessageComponent{discordgo.Container{AccentColor: &accent, Components: children}}
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

func (g *Gateway) sendActorResponse(ctx context.Context, channelID, actor, jobID, text string) error {
	for _, page := range paginateActorText(text, actorTraceMaxTextRunes) {
		label := "🤖 Main Agent"
		if actor == "subagent" {
			label = workerJobLabel(jobID)
		}
		accent := actorTraceAccentBlurple
		container := discordgo.Container{AccentColor: &accent, Components: []discordgo.MessageComponent{discordgo.TextDisplay{Content: label + "\n\n" + page}}}
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
