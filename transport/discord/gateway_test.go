package discord

import (
	"strings"
	"testing"

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
	embed := toolTraceEmbed([]string{"hello"})
	if embed.Title != "" {
		t.Fatalf("embed title = %q, want empty", embed.Title)
	}
	if embed.Description != "hello" {
		t.Fatalf("embed description = %q", embed.Description)
	}
}

func TestToolTraceStateStartsNewEmbedOnTextTransition(t *testing.T) {
	state := &toolTraceState{messageID: "msg-1", items: []string{"tool_call"}, isText: false}
	if isNew := state.append("hello", true); !isNew {
		t.Fatal("text after trace items should start a new embed")
	}
	if len(state.items) != 1 || state.items[0] != "hello" {
		t.Fatalf("new embed items = %q", state.items)
	}
	if state.messageID != "" {
		t.Fatalf("messageID should reset, got %q", state.messageID)
	}
}

func TestToolTraceStateConcatenatesConsecutiveText(t *testing.T) {
	state := &toolTraceState{}
	if isNew := state.append("hel", true); !isNew {
		t.Fatal("first text should request a new embed")
	}
	state.messageID = "msg-1"
	if isNew := state.append("lo", true); isNew {
		t.Fatal("consecutive text should edit the same embed")
	}
	if len(state.items) != 1 || state.items[0] != "hello" {
		t.Fatalf("text items = %q", state.items)
	}
}

func TestToolTraceStateStartsNewEmbedOnTraceAfterText(t *testing.T) {
	state := &toolTraceState{messageID: "msg-1", items: []string{"hello"}, isText: true}
	if isNew := state.append("tool_call", false); !isNew {
		t.Fatal("trace after text should start a new embed")
	}
	if len(state.items) != 1 || state.items[0] != "tool_call" {
		t.Fatalf("new embed items = %q", state.items)
	}
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
