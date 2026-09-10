package discord

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func TestToInputNormalizesDiscordMessage(t *testing.T) {
	message := InputMessage{SessionID: "discord:channel:1", ChannelID: "channel-1", MessageID: "message-1", AuthorID: "user-1", AuthorName: "Yuuta", Content: "hello"}
	input := ToInput(message)
	if input.Source != "discord" || input.SessionID != message.SessionID { t.Fatalf("unexpected input identity: %+v", input) }
	if input.Turn.Role != sdk.RoleUser || len(input.Turn.Content) != 1 || input.Turn.Content[0].Text != message.Content { t.Fatalf("unexpected canonical turn: %+v", input.Turn) }
	if input.Metadata["channel_id"] != message.ChannelID || input.Metadata["author_id"] != message.AuthorID { t.Fatalf("unexpected metadata: %+v", input.Metadata) }
}

func TestDisplaySendsRawDiscordOutput(t *testing.T) {
	sender := &recordingSender{}
	display := Display{Sender: sender}
	output := sdk.Output{Source: "discord", SessionID: "discord:channel:1", Metadata: map[string]string{"channel_id": "channel-1"}, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "hello"}}}
	if display.Source() != "discord" { t.Fatalf("Source() = %q", display.Source()) }
	if err := display.Display(context.Background(), output); err != nil { t.Fatal(err) }
	if sender.channelID != "channel-1" { t.Fatalf("unexpected channel: %q", sender.channelID) }
	var got sdk.Output
	if err := json.Unmarshal([]byte(sender.content), &got); err != nil { t.Fatalf("display did not send JSON output: %v", err) }
	if got.Source != output.Source || got.SessionID != output.SessionID || len(got.Content) != 1 || got.Content[0].Text != "hello" { t.Fatalf("unexpected raw output: %+v", got) }
}

type recordingSender struct{ channelID, content string }
func (s *recordingSender) SendMessage(_ context.Context, channelID, content string) error { s.channelID = channelID; s.content = content; return nil }

func TestDisplaySendsTraceResponseAsText(t *testing.T) {
	sender := &recordingSender{}
	display := Display{Sender: sender}
	trace := sdk.TraceEvent{Stage: sdk.TraceResponse, Response: &sdk.Response{Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "hello"}}}}
	output := sdk.Output{Source: "discord", SessionID: "discord:channel:1", Metadata: map[string]string{"channel_id": "channel-1"}, Trace: &trace}
	if err := display.Display(context.Background(), output); err != nil { t.Fatal(err) }
	if sender.channelID != "channel-1" { t.Fatalf("unexpected channel: %q", sender.channelID) }
	if sender.content != "hello" { t.Fatalf("trace response should send plain text, got %q", sender.content) }
}

func TestDisplaySkipsEmptyTraceResponse(t *testing.T) {
	sender := &recordingSender{}
	display := Display{Sender: sender}
	trace := sdk.TraceEvent{Stage: sdk.TraceResponse, Response: &sdk.Response{}}
	output := sdk.Output{Source: "discord", SessionID: "discord:channel:1", Metadata: map[string]string{"channel_id": "channel-1"}, Trace: &trace}
	if err := display.Display(context.Background(), output); err != nil { t.Fatal(err) }
	if sender.content != "" { t.Fatalf("empty trace response should send nothing, got %q", sender.content) }
}

func TestDisplayTraceRequiresChannel(t *testing.T) {
	display := Display{Sender: &recordingSender{}}
	trace := sdk.TraceEvent{Stage: sdk.TraceResponse, Response: &sdk.Response{Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "hello"}}}}
	output := sdk.Output{Source: "discord", SessionID: "discord:channel:1", Trace: &trace}
	if err := display.Display(context.Background(), output); err == nil {
		t.Fatal("expected missing channel_id error")
	}
}
