package discord

import (
	"context"
	"strings"
	"testing"

	"github.com/Tulipskun/ai/sdk"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestNormalizeMessageIgnoresBotMessages(t *testing.T) {
	_, ok := normalizeMessage(discordMessage{
		ID: "message-1", ChannelID: "channel-1", AuthorID: "bot-1", AuthorName: "bot", Content: "hello", AuthorIsBot: true,
	})
	if ok {
		t.Fatal("bot-authored message was accepted")
	}
}

func TestNormalizeMessageBuildsConversationSessionID(t *testing.T) {
	got, ok := normalizeMessage(discordMessage{
		ID: "message-1", ChannelID: "channel-1", AuthorID: "user-1", AuthorName: "Yuuta", Content: "hello",
	})
	if !ok {
		t.Fatal("message was rejected")
	}
	if got.SessionID != "discord:channel:channel-1" {
		t.Fatalf("SessionID = %q", got.SessionID)
	}
	if got.ChannelID != "channel-1" || got.Content != "hello" {
		t.Fatalf("unexpected normalized message: %+v", got)
	}
}

func TestGatewayIntentsIncludeMessageContentAndMessages(t *testing.T) {
	intents := gatewayIntents()
	if intents&discordgo.IntentsGuildMessages == 0 {
		t.Fatal("guild message intent is missing")
	}
	if intents&discordgo.IntentsDirectMessages == 0 {
		t.Fatal("direct message intent is missing")
	}
	if intents&discordgo.IntentsMessageContent == 0 {
		t.Fatal("message content intent is missing")
	}
}

func TestToolTraceEmbedHasNoTitle(t *testing.T) {
	embed := toolTraceEmbed([]string{"hello"}, "")
	if embed.Title != "" {
		t.Fatalf("embed title = %q, want empty", embed.Title)
	}
	if embed.Description != "hello" {
		t.Fatalf("embed description = %q", embed.Description)
	}
}

func TestToolTraceStateMergesConsecutiveText(t *testing.T) {
	state := &toolTraceState{}
	state.append("hel", true)
	state.append("lo", true)
	if len(state.items) != 1 || state.items[0] != "hello" {
		t.Fatalf("text items = %q", state.items)
	}
}

func TestToolTraceStateKeepsToolAndTextLinesSeparate(t *testing.T) {
	state := &toolTraceState{}
	state.append("tool_call", false)
	state.append("hel", true)
	state.append("lo", true)
	if len(state.items) != 2 || state.items[0] != "tool_call" || state.items[1] != "hello" {
		t.Fatalf("items = %q", state.items)
	}
}

func TestToolTraceStateUpdateReplacesNewestMatch(t *testing.T) {
	state := &toolTraceState{}
	state.append("sending request to provider", false)
	state.append(`tool_a("{}")`, false)
	state.append(`tool_b("{}")`, false)
	if !state.update(`❌ tool_a("{}") · 1s`, func(item string) bool { return strings.HasPrefix(item, "tool_a(") }) {
		t.Fatal("expected the tool_a line to update in place")
	}
	if state.items[1] != `❌ tool_a("{}") · 1s` || state.items[2] != `tool_b("{}")` {
		t.Fatalf("items = %q", state.items)
	}
	if state.update("x", nil) { t.Fatal("nil matcher must not update") }
	if state.update("x", func(string) bool { return false }) { t.Fatal("unmatched item must not update") }
}

func TestTraceFlushDelaySpacesSnapshots(t *testing.T) {
	if got := traceFlushDelay(time.Time{}); got != 0 { t.Fatalf("first snapshot should push immediately, got %v", got) }
	if got := traceFlushDelay(time.Now().Add(-2 * traceFlushInterval)); got != 0 { t.Fatalf("overdue snapshot should push immediately, got %v", got) }
	if got := traceFlushDelay(time.Now()); got <= 0 || got > traceFlushInterval {
		t.Fatalf("recent push should space the next snapshot by one interval, got %v", got)
	}
}

func TestAppendTraceItemStartsFreshEmbedOnModeSwitch(t *testing.T) {
	g := &Gateway{toolTrace: map[string]*toolTraceState{}}
	if err := g.appendToolTrace(context.Background(), "c1", "tool_call"); err != nil { t.Fatal(err) }
	if err := g.appendTextTrace(context.Background(), "c1", "hello"); err != nil { t.Fatal(err) }
	g.toolTraceMu.Lock(); defer g.toolTraceMu.Unlock()
	state := g.toolTrace["c1"]
	if state == nil || len(state.items) != 1 || state.items[0] != "hello" {
		t.Fatalf("items = %q", state.items)
	}
	if state.messageID != "" { t.Fatalf("mode switch should reset messageID, got %q", state.messageID) }
}

func TestUpdateToolTraceReplacesPendingRequestLine(t *testing.T) {
	g := &Gateway{toolTrace: map[string]*toolTraceState{}}
	if err := g.updateToolTrace(context.Background(), "c1", "sending request to provider", pendingRequestLine); err != nil { t.Fatal(err) }
	if err := g.updateToolTrace(context.Background(), "c1", "provider accepted request; processing · 1s", pendingRequestLine); err != nil { t.Fatal(err) }
	g.toolTraceMu.Lock(); defer g.toolTraceMu.Unlock()
	state := g.toolTrace["c1"]
	if state == nil || len(state.items) != 1 || state.items[0] != "provider accepted request; processing · 1s" {
		t.Fatalf("items = %q", state.items)
	}
}

func TestFlushToolTraceWithoutStateIsNoop(t *testing.T) {
	g := &Gateway{toolTrace: map[string]*toolTraceState{}}
	if err := g.flushToolTrace(context.Background(), "missing"); err != nil { t.Fatal(err) }
}

func TestToolTraceStateTrimsOldestOverBudget(t *testing.T) {
	state := &toolTraceState{}
	item := strings.Repeat("x", 500)
	for i := 0; i < 20; i++ {
		state.append(item, false)
	}
	if embedChars(state.items) > maxEmbedChars {
		t.Fatalf("embed still over budget: %d chars in %d items", embedChars(state.items), len(state.items))
	}
	if len(state.items) < 2 {
		t.Fatalf("trimmed too aggressively: %d items", len(state.items))
	}
	last := state.items[len(state.items)-1]
	if last != item {
		t.Fatal("newest item was not preserved")
	}
}

func TestToolTraceStateKeepsSingleLargeTextItem(t *testing.T) {
	state := &toolTraceState{}
	big := strings.Repeat("y", maxTextTraceLength)
	state.append(big, true)
	if len(state.items) != 1 {
		t.Fatalf("single text item should survive trim: %d items", len(state.items))
	}
}

func TestResetToolTraceStartsFresh(t *testing.T) {
	g := &Gateway{toolTrace: map[string]*toolTraceState{"c1": {messageID: "m1", items: []string{"old"}, isText: true}}}
	g.resetToolTrace("c1")
	if _, ok := g.toolTrace["c1"]; ok {
		t.Fatal("channel trace state should be cleared for the next turn")
	}
	g.resetToolTrace("")
}

func TestIsUnknownMessage(t *testing.T) {
	err := &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: 10008}}
	if !isUnknownMessage(err) {
		t.Fatal("10008 should count as unknown message")
	}
	other := &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: 50035}}
	if isUnknownMessage(other) {
		t.Fatal("50035 should not count as unknown message")
	}
	if isUnknownMessage(nil) {
		t.Fatal("nil should not count as unknown message")
	}
}

func TestTurnFooterLifecycle(t *testing.T) {
	g := &Gateway{toolTrace: map[string]*toolTraceState{}}
	g.startTurnFooter("c1")
	state := g.toolTrace["c1"]
	if state == nil || !state.footerActive || state.turnStart.IsZero() {
		t.Fatalf("footer not started: %+v", state)
	}
	if state.footerTimer == nil {
		t.Fatal("footer tick was not scheduled")
	}
	g.updateTurnFooterUsage("c1", sdk.Usage{InputTokens: 100, CacheReadTokens: 10, OutputTokens: 5})
	if state.turnUsage.InputTokens != 100 || state.turnUsage.CacheReadTokens != 10 || state.turnUsage.OutputTokens != 5 {
		t.Fatalf("usage not stored: %+v", state.turnUsage)
	}
	g.stopTurnFooter("c1")
	if state.footerActive {
		t.Fatal("footer should stop")
	}
	if state.footerTimer != nil {
		t.Fatal("footer tick should stop")
	}
}
