package discord

import (
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
