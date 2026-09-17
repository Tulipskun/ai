package discord

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tulipskun/ai/sdk"
	"github.com/bwmarrin/discordgo"
)

type v2Capture struct {
	sent   [][]discordgo.MessageComponent
	edited map[string][][]discordgo.MessageComponent
	nextID int
}

func newV2Capture(g *Gateway) *v2Capture {
	c := &v2Capture{edited: map[string][][]discordgo.MessageComponent{}}
	g.v2Send = func(_ context.Context, _ string, components []discordgo.MessageComponent) (string, error) {
		c.nextID++
		id := string(rune('a' + c.nextID - 1))
		c.sent = append(c.sent, components)
		return id, nil
	}
	g.v2Edit = func(_ context.Context, _, messageID string, components []discordgo.MessageComponent) error {
		c.edited[messageID] = append(c.edited[messageID], components)
		return nil
	}
	return c
}

func v2Texts(components []discordgo.MessageComponent) string {
	var b strings.Builder
	for _, component := range components {
		container, ok := component.(discordgo.Container)
		if !ok {
			continue
		}
		if container.AccentColor != nil {
			b.WriteString("accent:" + itoa(*container.AccentColor) + "\n")
		}
		for _, child := range container.Components {
			if text, ok := child.(discordgo.TextDisplay); ok {
				b.WriteString(text.Content)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}

func actorKeys(g *Gateway, channelID, actor, jobID string) (string, string) {
	chKey := chKeyFor(g, channelID)
	return chKey, chKey + "\x00" + actor + "\x00" + jobID
}

func chKeyFor(g *Gateway, channelID string) string {
	return strings.Join([]string{"chan", channelID}, "\x00")
}

func resetActorTrace() {
	actorTraceMu.Lock()
	actorTraceStates = make(map[string]*actorTraceState)
	actorTraceChannelSeq = make(map[string]int64)
	actorTraceMu.Unlock()
}

func TestActorTraceSeparateAccentColors(t *testing.T) {
	g := &Gateway{}
	capture := newV2Capture(g)
	resetActorTrace()
	ctx := context.Background()
	_, mainKey := actorKeys(g, "c1", "main", "")
	chKey, _ := actorKeys(g, "c1", "main", "")
	if err := g.displayActorTrace(ctx, "c1", chKey, mainKey, "main", "", sdk.TraceEvent{Stage: sdk.TraceRequest}); err != nil {
		t.Fatal(err)
	}
	if err := g.actorTraceFlush(ctx, "c1", chKey, mainKey); err != nil {
		t.Fatal(err)
	}
	_, workerKey := actorKeys(g, "c1", "subagent", "sa-123456789")
	if err := g.displayActorTrace(ctx, "c1", chKey, workerKey, "subagent", "sa-123456789", sdk.TraceEvent{Stage: sdk.TraceRequest}); err != nil {
		t.Fatal(err)
	}
	if err := g.actorTraceFlush(ctx, "c1", chKey, workerKey); err != nil {
		t.Fatal(err)
	}
	if len(capture.sent) != 2 {
		t.Fatalf("expected one message per actor: %d", len(capture.sent))
	}
	mainText := v2Texts(capture.sent[0])
	workerText := v2Texts(capture.sent[1])
	if !strings.Contains(mainText, "accent:"+itoa(mainTraceAccent)) || !strings.Contains(mainText, "Main Agent") {
		t.Fatalf("main stream color/label wrong: %s", mainText)
	}
	if !strings.Contains(workerText, "accent:"+itoa(subTraceAccent)) || !strings.Contains(workerText, "worker …456789") {
		t.Fatalf("worker stream color/label wrong: %s", workerText)
	}
}

func TestActorTraceShowsArgsAndMergedTiming(t *testing.T) {
	g := &Gateway{}
	_ = newV2Capture(g)
	resetActorTrace()
	ctx := context.Background()
	chKey, key := actorKeys(g, "c1", "main", "")
	events := []sdk.TraceEvent{
		{Stage: sdk.TraceRequest, RequestStartedMs: 1000, AtMs: 1000},
		{Stage: sdk.TraceProviderReady, RequestStartedMs: 1000, ProviderAcceptedMs: 1200, AtMs: 1200},
		{Stage: sdk.TraceToolCall, ToolCall: &sdk.ToolCall{ID: "t1", Name: "read_file", Arguments: "{\"path\":\"a.txt\"}"}, RequestStartedMs: 1000, ProviderAcceptedMs: 1200, AtMs: 1200},
		{Stage: sdk.TraceToolResult, ToolCall: &sdk.ToolCall{ID: "t1", Name: "read_file", Arguments: "{\"path\":\"a.txt\"}"}, ToolResult: &sdk.ToolResult{ID: "t1", Content: "file body secret"}, RequestStartedMs: 1000, ProviderAcceptedMs: 1200, AtMs: 1400},
	}
	for _, event := range events {
		if err := g.displayActorTrace(ctx, "c1", chKey, key, "main", "", event); err != nil {
			t.Fatal(err)
		}
	}
	state := actorTraceStates[key]
	if len(state.items) != 1 {
		t.Fatalf("request/accepted/tool must collapse into one line: %#v", state.items)
	}
	line := state.items[0]
	if !strings.HasPrefix(line, "✅ read_file") {
		t.Fatalf("tool line wrong: %q", line)
	}
	if !strings.Contains(line, `{"path":"a.txt"}`) {
		t.Fatalf("args must be visible: %q", line)
	}
	if !strings.Contains(line, "provider 200ms") || !strings.Contains(line, "total 400ms") {
		t.Fatalf("timing must come from subtracted timestamps: %q", line)
	}
	if strings.Contains(line, "secret") || strings.Contains(line, "sending") || strings.Contains(line, "accepted") {
		t.Fatalf("raw result or stale request rows leaked: %q", line)
	}
}

func TestActorTraceLongArgsTruncated(t *testing.T) {
	long := `{"command":"` + strings.Repeat("x", 400) + `"}`
	line := formatTraceToolLine(&sdk.ToolCall{Name: "run_command", Arguments: long}, "🔧", sdk.TraceEvent{})
	if !strings.Contains(line, "…") || len([]rune(line)) > 80+actorTraceArgsRunes+40 {
		t.Fatalf("args not truncated safely: %q", line)
	}
}

func TestActorTraceContentForcesNewSegmentBelow(t *testing.T) {
	g := &Gateway{}
	capture := newV2Capture(g)
	resetActorTrace()
	ctx := context.Background()
	chKey, key := actorKeys(g, "c1", "main", "")
	call := sdk.TraceEvent{Stage: sdk.TraceToolCall, ToolCall: &sdk.ToolCall{ID: "t1", Name: "read_file", Arguments: `{"path":"a"}`}, RequestStartedMs: 1000, ProviderAcceptedMs: 1500, AtMs: 1500}
	if err := g.displayActorTrace(ctx, "c1", chKey, key, "main", "", sdk.TraceEvent{Stage: sdk.TraceRequest}); err != nil {
		t.Fatal(err)
	}
	if err := g.displayActorTrace(ctx, "c1", chKey, key, "main", "", call); err != nil {
		t.Fatal(err)
	}
	if err := g.displayActorTrace(ctx, "c1", chKey, key, "main", "", sdk.TraceEvent{Stage: sdk.TraceResponseContent, Text: "here is the answer"}); err != nil {
		t.Fatal(err)
	}
	nextCall := sdk.TraceEvent{Stage: sdk.TraceToolCall, ToolCall: &sdk.ToolCall{ID: "t2", Name: "run_command", Arguments: `{"command":"go test"}`}, RequestStartedMs: 2000, ProviderAcceptedMs: 2500, AtMs: 2500}
	if err := g.displayActorTrace(ctx, "c1", chKey, key, "main", "", nextCall); err != nil {
		t.Fatal(err)
	}
	if err := g.actorTraceFlush(ctx, "c1", chKey, key); err != nil {
		t.Fatal(err)
	}
	// Unified main panel: tools plus content together in one message.
	if len(capture.sent) != 1 {
		t.Fatalf("expected unified panel with 1 message: %d", len(capture.sent))
	}
	items := actorTraceStates[key].items
	joined := strings.Join(items, "\n")
	if !strings.Contains(joined, "read_file") || !strings.Contains(joined, "go test") || !strings.Contains(joined, "here is the answer") {
		t.Fatalf("unified panel must hold tools plus content together: %#v", items)
	}
}

func TestActorTraceWorkerSegmentsKeepMainOrdering(t *testing.T) {
	g := &Gateway{}
	capture := newV2Capture(g)
	resetActorTrace()
	ctx := context.Background()
	chKey, mainKey := actorKeys(g, "c1", "main", "")
	_, workerKey := actorKeys(g, "c1", "subagent", "sa-55")
	if err := g.displayActorTrace(ctx, "c1", chKey, mainKey, "main", "", sdk.TraceEvent{Stage: sdk.TraceRequest}); err != nil {
		t.Fatal(err)
	}
	if err := g.displayActorTrace(ctx, "c1", chKey, mainKey, "main", "", sdk.TraceEvent{Stage: sdk.TraceToolCall, ToolCall: &sdk.ToolCall{ID: "d", Name: "delegate_to_subagent", Arguments: `{"task":"t"}`}}); err != nil {
		t.Fatal(err)
	}
	if err := g.displayActorTrace(ctx, "c1", chKey, workerKey, "subagent", "sa-55", sdk.TraceEvent{Stage: sdk.TraceRequest}); err != nil {
		t.Fatal(err)
	}
	if err := g.displayActorTrace(ctx, "c1", chKey, workerKey, "subagent", "sa-55", sdk.TraceEvent{Stage: sdk.TraceToolCall, ToolCall: &sdk.ToolCall{ID: "w", Name: "run_command", Arguments: `{"command":"go build"}`}}); err != nil {
		t.Fatal(err)
	}
	if err := g.displayActorTrace(ctx, "c1", chKey, workerKey, "subagent", "sa-55", sdk.TraceEvent{Stage: sdk.TraceToolResult, ToolCall: &sdk.ToolCall{ID: "w", Name: "run_command"}, ToolResult: &sdk.ToolResult{ID: "w"}}); err != nil {
		t.Fatal(err)
	}
	if err := g.displayActorTrace(ctx, "c1", chKey, workerKey, "subagent", "sa-55", sdk.TraceEvent{Stage: sdk.TraceResponse, Response: &sdk.Response{}}); err != nil {
		t.Fatal(err)
	}
	if err := g.displayActorTrace(ctx, "c1", chKey, mainKey, "main", "", sdk.TraceEvent{Stage: sdk.TraceToolResult, ToolCall: &sdk.ToolCall{ID: "d", Name: "delegate_to_subagent"}, ToolResult: &sdk.ToolResult{ID: "d"}}); err != nil {
		t.Fatal(err)
	}
	if err := g.actorTraceFlush(ctx, "c1", chKey, mainKey); err != nil {
		t.Fatal(err)
	}
	if len(capture.sent) != 3 {
		t.Fatalf("expected main1, worker, main2: %d", len(capture.sent))
	}
	mainResult := v2Texts(capture.sent[2])
	if !strings.Contains(mainResult, "✅ delegate_to_subagent") {
		t.Fatalf("main delegate result missing: %s", mainResult)
	}
	if len(capture.edited["a"]) != 0 {
		t.Fatalf("stale main message must not be edited: %v", capture.edited["a"])
	}
}

func TestActorTraceCompletedTurnStartsFresh(t *testing.T) {
	g := &Gateway{}
	capture := newV2Capture(g)
	resetActorTrace()
	ctx := context.Background()
	chKey, key := actorKeys(g, "c1", "main", "")
	if err := g.displayActorTrace(ctx, "c1", chKey, key, "main", "", sdk.TraceEvent{Stage: sdk.TraceRequest}); err != nil {
		t.Fatal(err)
	}
	if err := g.actorTraceFlush(ctx, "c1", chKey, key); err != nil {
		t.Fatal(err)
	}
	if err := g.displayActorTrace(ctx, "c1", chKey, key, "main", "", sdk.TraceEvent{Stage: sdk.TraceResponse}); err != nil {
		t.Fatal(err)
	}
	if !actorTraceStates[key].completed {
		t.Fatal("turn must complete after TraceResponse")
	}
	if err := g.displayActorTrace(ctx, "c1", chKey, key, "main", "", sdk.TraceEvent{Stage: sdk.TraceRequest}); err != nil {
		t.Fatal(err)
	}
	state := actorTraceStates[key]
	if state.messageID != "" || state.completed || len(state.items) != 1 {
		t.Fatalf("completed turn state not reset: %+v", state)
	}
	if len(capture.edited) != 0 {
		t.Fatalf("fresh turn must not edit the closed message: %v", capture.edited)
	}
}

func TestActorTraceTimingHelpers(t *testing.T) {
	trace := sdk.TraceEvent{RequestStartedMs: 1000, ProviderAcceptedMs: 2500, AtMs: 4000}
	if d := eventProviderLatency(trace); d != 1500*time.Millisecond {
		t.Fatalf("provider latency=%s", d)
	}
	if d := eventElapsed(trace); d != 3000*time.Millisecond {
		t.Fatalf("total=%s", d)
	}
	if d := eventElapsed(sdk.TraceEvent{Elapsed: 3 * time.Second}); d != 3*time.Second {
		t.Fatalf("fallback elapsed=%s", d)
	}
}

func TestActorTraceProviderAcceptedBecomesContentWhenAlone(t *testing.T) {
	g := &Gateway{}
	capture := newV2Capture(g)
	resetActorTrace()
	ctx := context.Background()
	chKey, key := actorKeys(g, "c1", "main", "")
	events := []sdk.TraceEvent{
		{Stage: sdk.TraceRequest, RequestStartedMs: 1000, AtMs: 1000},
		{Stage: sdk.TraceProviderReady, RequestStartedMs: 1000, ProviderAcceptedMs: 1200, AtMs: 1200},
		{Stage: sdk.TraceResponseContent, Text: "final answer here"},
	}
	// The accepted line is still pending (no flush yet) - the response must
	// edit the single container into the content instead of stacking.
	if err := g.displayActorTrace(ctx, "c1", chKey, key, "main", "", events[0]); err != nil {
		t.Fatal(err)
	}
	if err := g.actorTraceFlush(ctx, "c1", chKey, key); err != nil {
		t.Fatal(err)
	}
	if err := g.displayActorTrace(ctx, "c1", chKey, key, "main", "", events[1]); err != nil {
		t.Fatal(err)
	}
	if err := g.displayActorTrace(ctx, "c1", chKey, key, "main", "", events[2]); err != nil {
		t.Fatal(err)
	}
	if len(capture.sent) != 1 {
		t.Fatalf("content must reuse the accepted-only message: sends=%d", len(capture.sent))
	}
	if len(capture.edited["a"]) != 1 {
		t.Fatalf("expected one in-place edit: %v", capture.edited)
	}
	final := v2Texts(capture.edited["a"][0])
	if !strings.Contains(final, "final answer here") || strings.Contains(final, "provider accepted") {
		t.Fatalf("transformed message wrong: %s", final)
	}
}

func TestActorTraceAcceptedDroppedWhenOtherLinesExist(t *testing.T) {
	g := &Gateway{}
	capture := newV2Capture(g)
	resetActorTrace()
	ctx := context.Background()
	chKey, key := actorKeys(g, "c1", "main", "")
	call := sdk.TraceEvent{Stage: sdk.TraceToolCall, ToolCall: &sdk.ToolCall{ID: "t", Name: "bash", Arguments: `{"command":"ls"}`}, RequestStartedMs: 1000, ProviderAcceptedMs: 1500, AtMs: 1500}
	if err := g.displayActorTrace(ctx, "c1", chKey, key, "main", "", sdk.TraceEvent{Stage: sdk.TraceRequest}); err != nil {
		t.Fatal(err)
	}
	if err := g.displayActorTrace(ctx, "c1", chKey, key, "main", "", call); err != nil {
		t.Fatal(err)
	}
	// A second request adds its own accepted marker; the response that follows
	// must strip that marker and land as a fresh message below.
	if err := g.displayActorTrace(ctx, "c1", chKey, key, "main", "", sdk.TraceEvent{Stage: sdk.TraceRequest, RequestStartedMs: 2000, AtMs: 2000}); err != nil {
		t.Fatal(err)
	}
	if err := g.displayActorTrace(ctx, "c1", chKey, key, "main", "", sdk.TraceEvent{Stage: sdk.TraceProviderReady, RequestStartedMs: 2000, ProviderAcceptedMs: 2200, AtMs: 2200}); err != nil {
		t.Fatal(err)
	}
	if err := g.displayActorTrace(ctx, "c1", chKey, key, "main", "", sdk.TraceEvent{Stage: sdk.TraceResponseContent, Text: "answer"}); err != nil {
		t.Fatal(err)
	}
	items := actorTraceStates[key].items
	for _, item := range items {
		if strings.HasPrefix(item, "⏳ ") {
			t.Fatalf("accepted marker must be removed when other lines exist: %#v", items)
		}
	}
	// Unified main panel: content appended into same message, tools plus content together.
	if len(capture.sent) != 1 {
		t.Fatalf("expected unified panel with 1 message: %d", len(capture.sent))
	}
	joined := strings.Join(items, "\n")
	if !strings.Contains(joined, "bash") || !strings.Contains(joined, "answer") {
		t.Fatalf("unified panel must hold tools plus content together: %#v", items)
	}
	edits := capture.edited["a"]
	if len(edits) == 0 {
		t.Fatal("unified message must be edited to drop the accepted marker and append content")
	}
	last := v2Texts(edits[len(edits)-1])
	if strings.Contains(last, "⏳") || !strings.Contains(last, "answer") {
		t.Fatalf("unified container must hold answer without accepted marker: %s", last)
	}
}

func TestActorTraceRetryShowsReason(t *testing.T) {
	g := &Gateway{}
	_ = newV2Capture(g)
	resetActorTrace()
	ctx := context.Background()
	chKey, key := actorKeys(g, "c1", "main", "")
	if err := g.displayActorTrace(ctx, "c1", chKey, key, "main", "", sdk.TraceEvent{Stage: sdk.TraceRetryWait, Err: displayHTTPError{429}, RetryAfter: 17 * time.Second}); err != nil {
		t.Fatal(err)
	}
	line := actorTraceStates[key].items[len(actorTraceStates[key].items)-1]
	if !strings.Contains(line, "retrying request") || !strings.Contains(line, "rate limited") || !strings.Contains(line, "429") {
		t.Fatalf("retry line must name the reason: %q", line)
	}
}
