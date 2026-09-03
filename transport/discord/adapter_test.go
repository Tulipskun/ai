package discord

import (
	"context"
	"strings"
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

func TestDisplayFormatsToolTracesAsOneLineEach(t *testing.T) {
	sender := &recordingMessagesSender{}
	display := Display{Sender: sender}
	metadata := map[string]string{"channel_id": "channel-1"}

	if err := display.Display(context.Background(), sdk.Output{
		Metadata: metadata,
		Trace: &sdk.TraceEvent{
			Stage: sdk.TraceToolCall,
			ToolCall: &sdk.ToolCall{
				ID:        "call-1",
				Name:      "search_memory",
				Arguments: "{\n  \"query\": \"hello\",\n  \"limit\": 8\n}",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := display.Display(context.Background(), sdk.Output{
		Metadata: metadata,
		Trace: &sdk.TraceEvent{
			Stage: sdk.TraceToolResult,
			ToolResult: &sdk.ToolResult{
				ID:      "call-1",
				Content: "line one\nline two\n{\"matches\":3}",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if len(sender.messages) != 2 {
		t.Fatalf("expected one message per trace event, got=%d", len(sender.messages))
	}
	for i, message := range sender.messages {
		if strings.ContainsAny(message, "\r\n") {
			t.Fatalf("trace message %d contains a newline: %q", i, message)
		}
	}
	if sender.messages[0] != "[AI tool_call] search_memory {\"query\": \"hello\", \"limit\": 8}" {
		t.Fatalf("unexpected tool call message: %q", sender.messages[0])
	}
	if sender.messages[1] != "[AI tool_result] line one line two {\"matches\":3}" {
		t.Fatalf("unexpected tool result message: %q", sender.messages[1])
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

	if err := display.Display(context.Background(), sdk.Output{
		Metadata: map[string]string{"channel_id": channelID},
		Trace:    &sdk.TraceEvent{Stage: sdk.TraceResponse, Response: &sdk.Response{Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "done"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	if sender.deleted != 1 {
		t.Fatalf("expected retry status cleanup, deleted=%d", sender.deleted)
	}
}

type recordingSender struct{ channelID, content string }

func (s *recordingSender) SendMessage(_ context.Context, channelID, content string) error {
	s.channelID = channelID
	s.content = content
	return nil
}

type recordingMessagesSender struct{ messages []string }

func (s *recordingMessagesSender) SendMessage(_ context.Context, _, content string) error {
	s.messages = append(s.messages, content)
	return nil
}

type statusRecordingSender struct {
	sent        int
	edited      int
	deleted     int
	lastContent string
	statusID    string
}

func (s *statusRecordingSender) SendMessage(_ context.Context, _, _ string) error { return nil }
func (s *statusRecordingSender) SendStatusMessage(_ context.Context, _, content string) (string, error) {
	s.sent++
	s.lastContent = content
	s.statusID = "status-1"
	return s.statusID, nil
}
func (s *statusRecordingSender) EditMessage(_ context.Context, _, _, content string) error {
	s.edited++
	s.lastContent = content
	return nil
}
func (s *statusRecordingSender) DeleteMessage(_ context.Context, _, _ string) error {
	s.deleted++
	return nil
}
func (s *statusRecordingSender) updateRetryStatus(ctx context.Context, channelID, content string) error {
	if s.statusID == "" {
		id, err := s.SendStatusMessage(ctx, channelID, content)
		if err != nil {
			return err
		}
		s.statusID = id
		return nil
	}
	return s.EditMessage(ctx, channelID, s.statusID, content)
}
func (s *statusRecordingSender) clearRetryStatus(ctx context.Context, channelID string) error {
	if s.statusID == "" {
		return nil
	}
	if err := s.DeleteMessage(ctx, channelID, s.statusID); err != nil {
		return err
	}
	s.statusID = ""
	return nil
}

type testHTTPStatusError struct{ code int }
func (e testHTTPStatusError) Error() string { return "test" }
func (e testHTTPStatusError) HTTPStatusCode() int { return e.code }
