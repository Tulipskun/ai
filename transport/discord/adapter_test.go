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

type routingFakeSender struct{ texts, tools, messages []string }

func (f *routingFakeSender) SendMessage(_ context.Context, channelID, content string) error {
	f.messages = append(f.messages, channelID+":"+content)
	return nil
}
func (f *routingFakeSender) setToolTrace(_ context.Context, _ string, _ []string) error { return nil }
func (f *routingFakeSender) appendToolTrace(_ context.Context, _ string, item string) error {
	f.tools = append(f.tools, item)
	return nil
}
func (f *routingFakeSender) clearToolTrace(_ context.Context, _ string) error { return nil }
func (f *routingFakeSender) appendTextTrace(_ context.Context, _ string, text string) error {
	f.texts = append(f.texts, text)
	return nil
}

func TestDisplayTraceContentRoutesTextToNewEmbed(t *testing.T) {
	sender := &routingFakeSender{}
	display := Display{Sender: sender}
	trace := sdk.TraceEvent{Stage: sdk.TraceResponseContent, Response: &sdk.Response{Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "hello"}}}}
	output := sdk.Output{Source: "discord", SessionID: "s", Metadata: map[string]string{"channel_id": "c"}, Trace: &trace}
	if err := display.Display(context.Background(), output); err != nil { t.Fatal(err) }
	if len(sender.texts) != 1 || sender.texts[0] != "hello" { t.Fatalf("texts = %q", sender.texts) }
	if len(sender.tools) != 0 || len(sender.messages) != 0 { t.Fatalf("text should only go to new embed: tools=%q messages=%q", sender.tools, sender.messages) }
}

func TestDisplayTraceContentPrefersStreamText(t *testing.T) {
	sender := &routingFakeSender{}
	display := Display{Sender: sender}
	trace := sdk.TraceEvent{Stage: sdk.TraceResponseContent, Text: "chunk"}
	output := sdk.Output{Source: "discord", SessionID: "s", Metadata: map[string]string{"channel_id": "c"}, Trace: &trace}
	if err := display.Display(context.Background(), output); err != nil { t.Fatal(err) }
	if len(sender.texts) != 1 || sender.texts[0] != "chunk" { t.Fatalf("texts = %q", sender.texts) }
}

func TestDisplayTraceContentEmptySendsNothing(t *testing.T) {
	sender := &routingFakeSender{}
	display := Display{Sender: sender}
	trace := sdk.TraceEvent{Stage: sdk.TraceResponseContent, Response: &sdk.Response{}}
	output := sdk.Output{Source: "discord", SessionID: "s", Metadata: map[string]string{"channel_id": "c"}, Trace: &trace}
	if err := display.Display(context.Background(), output); err != nil { t.Fatal(err) }
	if len(sender.texts) != 0 || len(sender.tools) != 0 || len(sender.messages) != 0 {
		t.Fatalf("empty content should send nothing: texts=%q tools=%q messages=%q", sender.texts, sender.tools, sender.messages)
	}
}

func TestTruncateTextPreservesNewlines(t *testing.T) {
	got := truncateText("a\nb", 100)
	if got != "a\nb" { t.Fatalf("truncateText = %q", got) }
	got = truncateText("abcdef", 3)
	if got != "ab…" { t.Fatalf("truncateText = %q", got) }
}
