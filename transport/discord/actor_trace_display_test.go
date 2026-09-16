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
		for _, child := range container.Components {
			if text, ok := child.(discordgo.TextDisplay); ok {
				b.WriteString(text.Content)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

func TestActorTraceRendersComponentsV2Container(t *testing.T) {
	g := &Gateway{}
	capture := newV2Capture(g)
	key := "trace-v2"
	actorTraceStates = make(map[string]*actorTraceState)
	if err := g.displayActorTrace(context.Background(), "c1", key, "main", "", sdk.TraceEvent{Stage: sdk.TraceRequest}); err != nil {
		t.Fatal(err)
	}
	if err := g.actorTraceFlush(context.Background(), "c1", key); err != nil {
		t.Fatal(err)
	}
	if len(capture.sent) != 1 {
		t.Fatalf("sends=%d", len(capture.sent))
	}
	container, ok := capture.sent[0][0].(discordgo.Container)
	if !ok || len(container.Components) < 2 {
		t.Fatalf("expected container with header + items: %+v", capture.sent[0])
	}
	if container.AccentColor == nil || *container.AccentColor != actorTraceAccentBlurple {
		t.Fatal("missing accent color")
	}
	if !strings.Contains(v2Texts(capture.sent[0]), "sending request to provider") {
		t.Fatalf("trace text: %s", v2Texts(capture.sent[0]))
	}
}

func TestActorTraceMergesProviderAcceptedIntoToolLines(t *testing.T) {
	g := &Gateway{}
	_ = newV2Capture(g)
	key := "trace-merge"
	actorTraceStates = make(map[string]*actorTraceState)
	ctx := context.Background()
	request := sdk.TraceEvent{Stage: sdk.TraceRequest, RequestStartedMs: 1000, AtMs: 1000}
	accepted := sdk.TraceEvent{Stage: sdk.TraceProviderReady, RequestStartedMs: 1000, ProviderAcceptedMs: 1200, AtMs: 1200}
	call := sdk.TraceEvent{Stage: sdk.TraceToolCall, ToolCall: &sdk.ToolCall{ID: "t1", Name: "read_file"}, RequestStartedMs: 1000, ProviderAcceptedMs: 1200, AtMs: 1200}
	result := sdk.TraceEvent{Stage: sdk.TraceToolResult, ToolCall: &sdk.ToolCall{ID: "t1", Name: "read_file"}, ToolResult: &sdk.ToolResult{ID: "t1"}, RequestStartedMs: 1000, ProviderAcceptedMs: 1200, AtMs: 1400}
	for _, event := range []sdk.TraceEvent{request, accepted, call, result} {
		if err := g.displayActorTrace(ctx, "c1", key, "main", "", event); err != nil {
			t.Fatal(err)
		}
	}
	state := actorTraceStates[key]
	if len(state.items) != 1 {
		t.Fatalf("items must collapse into one tool line: %#v", state.items)
	}
	line := state.items[0]
	if !strings.HasPrefix(line, "🤖 ✅ tool: read_file") {
		t.Fatalf("tool line wrong: %q", line)
	}
	if !strings.Contains(line, "provider 200ms") || !strings.Contains(line, "total 400ms") {
		t.Fatalf("tool line must merge provider latency and request-to-completion total: %q", line)
	}
	if strings.Contains(line, "accepted") {
		t.Fatalf("accepted marker must be replaced by the tool line: %q", line)
	}
}

func TestActorTraceOrdersMainBeforeWorker(t *testing.T) {
	g := &Gateway{}
	_ = newV2Capture(g)
	key := "trace-order"
	actorTraceStates = make(map[string]*actorTraceState)
	ctx := context.Background()
	events := []struct {
		actor, jobID string
		event        sdk.TraceEvent
	}{
		{"main", "", sdk.TraceEvent{Stage: sdk.TraceRequest, RequestStartedMs: 1000, AtMs: 1000}},
		{"main", "", sdk.TraceEvent{Stage: sdk.TraceToolCall, ToolCall: &sdk.ToolCall{ID: "d1", Name: "delegate_to_subagent"}, RequestStartedMs: 1000, ProviderAcceptedMs: 1500, AtMs: 1500}},
		{"subagent", "sa-77", sdk.TraceEvent{Stage: sdk.TraceRequest, RequestStartedMs: 2000, AtMs: 2000}},
		{"subagent", "sa-77", sdk.TraceEvent{Stage: sdk.TraceToolCall, ToolCall: &sdk.ToolCall{ID: "w1", Name: "run_command"}, RequestStartedMs: 2000, ProviderAcceptedMs: 2500, AtMs: 2500}},
		{"subagent", "sa-77", sdk.TraceEvent{Stage: sdk.TraceToolResult, ToolCall: &sdk.ToolCall{ID: "w1", Name: "run_command"}, ToolResult: &sdk.ToolResult{ID: "w1"}, RequestStartedMs: 2000, ProviderAcceptedMs: 2500, AtMs: 6000}},
		{"main", "", sdk.TraceEvent{Stage: sdk.TraceToolResult, ToolCall: &sdk.ToolCall{ID: "d1", Name: "delegate_to_subagent"}, ToolResult: &sdk.ToolResult{ID: "d1"}, RequestStartedMs: 1000, ProviderAcceptedMs: 1500, AtMs: 7000}},
	}
	for _, item := range events {
		if err := g.displayActorTrace(ctx, "c1", key, item.actor, item.jobID, item.event); err != nil {
			t.Fatal(err)
		}
	}
	items := actorTraceStates[key].items
	if len(items) != 3 {
		t.Fatalf("unexpected stream: %#v", items)
	}
	if !strings.HasPrefix(items[0], "🤖 ✅ tool: delegate_to_subagent") {
		t.Fatalf("planner delegate line must come first: %#v", items)
	}
	if !strings.HasPrefix(items[1], "🛠 worker …77") {
		t.Fatalf("worker header missing: %#v", items)
	}
	if !strings.Contains(items[2], "✅ tool: run_command") || !strings.Contains(items[2], "total 4s") {
		t.Fatalf("worker completion line wrong: %#v", items)
	}
	if strings.Contains(items[2], "sending") || strings.Contains(items[2], "accepted") {
		t.Fatalf("worker pending lines must be merged away: %#v", items)
	}
}

func TestActorTraceCompletedTurnStartsNewMessage(t *testing.T) {
	g := &Gateway{}
	capture := newV2Capture(g)
	key := "trace-reset"
	actorTraceStates = make(map[string]*actorTraceState)
	ctx := context.Background()
	if err := g.displayActorTrace(ctx, "c1", key, "main", "", sdk.TraceEvent{Stage: sdk.TraceRequest}); err != nil {
		t.Fatal(err)
	}
	if err := g.actorTraceFlush(ctx, "c1", key); err != nil {
		t.Fatal(err)
	}
	messageID := actorTraceStates[key].messageID
	if messageID == "" || len(capture.sent) != 1 {
		t.Fatalf("first message not created: %q", messageID)
	}
	if err := g.displayActorTrace(ctx, "c1", key, "main", "", sdk.TraceEvent{Stage: sdk.TraceResponse}); err != nil {
		t.Fatal(err)
	}
	if !actorTraceStates[key].completed {
		t.Fatal("turn must complete after TraceResponse")
	}
	if err := g.displayActorTrace(ctx, "c1", key, "main", "", sdk.TraceEvent{Stage: sdk.TraceRequest}); err != nil {
		t.Fatal(err)
	}
	if err := g.actorTraceFlush(ctx, "c1", key); err != nil {
		t.Fatal(err)
	}
	if len(capture.sent) != 2 {
		t.Fatalf("completed turn must open a new message: sends=%d edits=%v", len(capture.sent), capture.edited)
	}
	if len(capture.edited[messageID]) != 0 {
		t.Fatal("new turn must not edit the completed message")
	}
}

func TestActorTraceWorkerLinesUseOwnRequestWindow(t *testing.T) {
	g := &Gateway{}
	_ = newV2Capture(g)
	key := "trace-windows"
	actorTraceStates = make(map[string]*actorTraceState)
	ctx := context.Background()
	if err := g.displayActorTrace(ctx, "c1", key, "subagent", "sa-9", sdk.TraceEvent{Stage: sdk.TraceRequest, RequestStartedMs: 10_000, AtMs: 10_000}); err != nil {
		t.Fatal(err)
	}
	if err := g.displayActorTrace(ctx, "c1", key, "subagent", "sa-9", sdk.TraceEvent{Stage: sdk.TraceProviderReady, RequestStartedMs: 10_000, ProviderAcceptedMs: 11_000, AtMs: 11_000}); err != nil {
		t.Fatal(err)
	}
	if err := g.displayActorTrace(ctx, "c1", key, "subagent", "sa-9", sdk.TraceEvent{Stage: sdk.TraceToolResult, ToolCall: &sdk.ToolCall{ID: "x", Name: "edit_file"}, ToolResult: &sdk.ToolResult{ID: "x"}, RequestStartedMs: 10_000, ProviderAcceptedMs: 11_000, AtMs: 12_000}); err != nil {
		t.Fatal(err)
	}
	line := actorTraceStates[key].items[len(actorTraceStates[key].items)-1]
	if !strings.Contains(line, "provider 1s") || !strings.Contains(line, "total 2s") {
		t.Fatalf("worker timing must subtract its own request window: %q", line)
	}
	if strings.Contains(line, "12.5s · 1s") {
		t.Fatalf("worker timing leaked absolute stamps: %q", line)
	}
}

func TestActorTraceTimingFallsBackToElapsed(t *testing.T) {
	trace := sdk.TraceEvent{Stage: sdk.TraceToolResult, Elapsed: 3 * time.Second}
	if d := eventElapsed(trace); d != 3*time.Second {
		t.Fatalf("fallback elapsed=%s", d)
	}
}
