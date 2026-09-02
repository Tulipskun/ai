package sdk

import (
	"context"
	"testing"
)

func TestDiscordInputConvertsMessageToCanonicalInput(t *testing.T) {
	got := DiscordInputMessage{
		SessionID: "session-1",
		ChannelID: "channel-1",
		MessageID: "message-1",
		AuthorID:  "user-1",
		AuthorName: "Yuuta",
		Content:   "hello",
	}

	input := DiscordToInput(got)
	if input.SessionID != got.SessionID || input.Source != "discord" {
		t.Fatalf("unexpected input metadata: %+v", input)
	}
	if len(input.Turn.Content) != 1 || input.Turn.Content[0].Text != "hello" {
		t.Fatalf("unexpected canonical turn: %+v", input.Turn)
	}
}

func TestDiscordDisplayUsesChannelMetadata(t *testing.T) {
	sender := &recordingDiscordSender{}
	display := DiscordDisplay{Sender: sender}
	output := Output{SessionID: "session-1", Source: "discord", Metadata: map[string]string{"channel_id": "channel-1"}, Content: []ContentPart{{Type: ContentText, Text: "hello"}}}

	if err := display.Display(context.Background(), output); err != nil {
		t.Fatal(err)
	}
	if sender.channelID != "channel-1" || sender.content != "hello" {
		t.Fatalf("unexpected discord send: channel=%q content=%q", sender.channelID, sender.content)
	}
}

type recordingDiscordSender struct { channelID, content string }
func (s *recordingDiscordSender) SendMessage(_ context.Context, channelID, content string) error { s.channelID, s.content = channelID, content; return nil }
