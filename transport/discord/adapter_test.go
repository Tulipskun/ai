package discord

import (
	"context"
	"testing"
	"time"

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

func TestDisplayUpdatesRetryStatusInOneMessage(t *testing.T) {
	sender := &statusRecordingSender{}
	display := Display{Sender: sender}
	channelID := "channel-1"

	for _, delay := range []time.Duration{3 * time.Second, 6 * time.Second, 12 * time.Second} {
		if err := display.Display(context.Background(), sdk.Output{
			Metadata: map[string]string{"channel_id": channelID},
			Trace: &sdk.TraceEvent{
				Stage:      sdk.TraceRetryWait,
				Err:        testHTTPStatusError{code: 429},
				RetryAfter: delay,
			},
		}); err != nil {
			t.Fatal(err)
		}
	}

	if sender.sent != 1 {
		t.Fatalf("expected one status message, sent=%d", sender.sent)
	}
	if sender.edited != 2 {
		t.Fatalf("expected two status edits, edited=%d", sender.edited)
	}
	if sender.lastContent != "[AI retry] test\nกำลังรอ 12s ก่อน retry" {
		t.Fatalf("unexpected final status: %q", sender.lastContent)
	}
}

type recordingSender struct{ channelID, content string }

func (s *recordingSender) SendMessage(_ context.Context, channelID, content string) error {
	s.channelID = channelID
	s.content = content
	return nil
}

type statusRecordingSender struct {
	sent        int
	edited      int
	lastContent string
}

func (s *statusRecordingSender) SendMessage(_ context.Context, _, _ string) error { return nil }
func (s *statusRecordingSender) SendStatusMessage(_ context.Context, _, content string) (string, error) {
	s.sent++
	s.lastContent = content
	return "status-1", nil
}
func (s *statusRecordingSender) EditMessage(_ context.Context, _, _, content string) error {
	s.edited++
	s.lastContent = content
	return nil
}

func (s *statusRecordingSender) DeleteMessage(_ context.Context, _, _ string) error { return nil }

type testHTTPStatusError struct{ code int }
func (e testHTTPStatusError) Error() string { return "test" }
func (e testHTTPStatusError) HTTPStatusCode() int { return e.code }
