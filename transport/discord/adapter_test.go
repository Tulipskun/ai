package discord

import (
	"context"
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func TestToInputNormalizesDiscordMessage(t *testing.T) {
	message := InputMessage{
		SessionID:  "discord:channel:1",
		ChannelID:  "channel-1",
		MessageID:  "message-1",
		AuthorID:   "user-1",
		AuthorName: "Yuuta",
		Content:    "hello",
	}

	input := ToInput(message)
	if input.Source != "discord" || input.SessionID != message.SessionID {
		t.Fatalf("unexpected input identity: %+v", input)
	}
	if input.Turn.Role != sdk.RoleUser || len(input.Turn.Content) != 1 || input.Turn.Content[0].Text != message.Content {
		t.Fatalf("unexpected canonical turn: %+v", input.Turn)
	}
	if input.Metadata["channel_id"] != message.ChannelID || input.Metadata["author_id"] != message.AuthorID {
		t.Fatalf("unexpected metadata: %+v", input.Metadata)
	}
}

func TestDisplaySendsOnlyDiscordOutput(t *testing.T) {
	sender := &recordingSender{}
	display := Display{Sender: sender}
	output := sdk.Output{
		Source:    "discord",
		SessionID: "discord:channel:1",
		Metadata:  map[string]string{"channel_id": "channel-1"},
		Content:   []sdk.ContentPart{{Type: sdk.ContentText, Text: "hello"}},
	}

	if display.Source() != "discord" {
		t.Fatalf("Source() = %q", display.Source())
	}
	if err := display.Display(context.Background(), output); err != nil {
		t.Fatal(err)
	}
	if sender.channelID != "channel-1" || sender.content != "hello" {
		t.Fatalf("unexpected send: channel=%q content=%q", sender.channelID, sender.content)
	}
}

type recordingSender struct{ channelID, content string }

func (s *recordingSender) SendMessage(_ context.Context, channelID, content string) error {
	s.channelID = channelID
	s.content = content
	return nil
}
