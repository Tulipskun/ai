package discord

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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

func TestDisplayTraceResponseSkipsPlainText(t *testing.T) {
	sender := &recordingSender{}
	display := Display{Sender: sender}
	trace := sdk.TraceEvent{Stage: sdk.TraceResponse, Response: &sdk.Response{Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "hello"}}}}
	output := sdk.Output{Source: "discord", SessionID: "discord:channel:1", Metadata: map[string]string{"channel_id": "channel-1"}, Trace: &trace}
	if err := display.Display(context.Background(), output); err != nil { t.Fatal(err) }
	if sender.content != "" { t.Fatalf("final trace should not send duplicate plain text, got %q", sender.content) }
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

type routingFakeSender struct{ texts, tools, messages []string; flushes int }

func (f *routingFakeSender) SendMessage(_ context.Context, channelID, content string) error {
	f.messages = append(f.messages, channelID+":"+content)
	return nil
}
func (f *routingFakeSender) setToolTrace(_ context.Context, _ string, _ []string) error { return nil }
func (f *routingFakeSender) appendToolTrace(_ context.Context, _ string, item string) error {
	f.tools = append(f.tools, item)
	return nil
}
func (f *routingFakeSender) updateToolTrace(_ context.Context, _ string, item string, match func(string) bool) error {
	for i := len(f.tools) - 1; i >= 0; i-- {
		if match != nil && match(f.tools[i]) {
			f.tools[i] = item
			return nil
		}
	}
	f.tools = append(f.tools, item)
	return nil
}
func (f *routingFakeSender) flushToolTrace(_ context.Context, _ string) error { f.flushes++; return nil }
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

func TestDisplayTraceReadyShowsElapsed(t *testing.T) {
	sender := &routingFakeSender{}
	display := Display{Sender: sender}
	trace := sdk.TraceEvent{Stage: sdk.TraceProviderReady, Elapsed: 23 * time.Second}
	output := sdk.Output{Source: "discord", SessionID: "s", Metadata: map[string]string{"channel_id": "c"}, Trace: &trace}
	if err := display.Display(context.Background(), output); err != nil { t.Fatal(err) }
	if len(sender.tools) != 1 || sender.tools[0] != "provider accepted request; processing · 23s" {
		t.Fatalf("tools = %q", sender.tools)
	}
}

func TestDisplayTraceToolShowsElapsed(t *testing.T) {
	sender := &routingFakeSender{}
	display := Display{Sender: sender}
	call := &sdk.ToolCall{ID: "1", Name: "list_directory", Arguments: "{}"}
	trace := sdk.TraceEvent{Stage: sdk.TraceToolCall, ToolCall: call, Elapsed: 5 * time.Second}
	output := sdk.Output{Source: "discord", SessionID: "s", Metadata: map[string]string{"channel_id": "c"}, Trace: &trace}
	if err := display.Display(context.Background(), output); err != nil { t.Fatal(err) }
	if len(sender.tools) != 1 || sender.tools[0] != `list_directory("{}") · 5s` {
		t.Fatalf("tools = %q", sender.tools)
	}
}

func TestWithElapsedSkipsZero(t *testing.T) {
	if got := withElapsed("msg", 0); got != "msg" { t.Fatalf("withElapsed = %q", got) }
	if got := withElapsed("msg", 1500*time.Millisecond); got != "msg · 2s" { t.Fatalf("withElapsed = %q", got) }
}

func displayTraceEvent(t *testing.T, display Display, trace sdk.TraceEvent) {
	t.Helper()
	output := sdk.Output{Source: "discord", SessionID: "s", Metadata: map[string]string{"channel_id": "c"}, Trace: &trace}
	if err := display.Display(context.Background(), output); err != nil { t.Fatal(err) }
}

func TestDisplayTraceRequestBecomesAcceptedLine(t *testing.T) {
	sender := &routingFakeSender{}
	display := Display{Sender: sender}
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceRequest})
	if len(sender.tools) != 1 || sender.tools[0] != "sending request to provider" { t.Fatalf("tools = %q", sender.tools) }
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceProviderReady, Elapsed: 23 * time.Second})
	if len(sender.tools) != 1 || sender.tools[0] != "provider accepted request; processing · 23s" { t.Fatalf("tools = %q", sender.tools) }
}

func TestDisplayTraceRetryKeepsOnePendingRequestLine(t *testing.T) {
	sender := &routingFakeSender{}
	display := Display{Sender: sender}
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceRequest})
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceRequest})
	if len(sender.tools) != 1 || sender.tools[0] != "sending request to provider" { t.Fatalf("tools = %q", sender.tools) }
}

func TestDisplayTraceToolFailureMarksLineInPlace(t *testing.T) {
	sender := &routingFakeSender{}
	display := Display{Sender: sender}
	call := &sdk.ToolCall{ID: "1", Name: "list_directory", Arguments: `{"path":"~/ai"}`}
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceToolCall, ToolCall: call, Elapsed: 2 * time.Second})
	if len(sender.tools) != 1 || sender.tools[0] != `list_directory("{\"path\":\"~/ai\"}") · 2s` { t.Fatalf("tools = %q", sender.tools) }
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceToolRunning, ToolCall: call, Elapsed: 2 * time.Second})
	if len(sender.tools) != 1 { t.Fatalf("tool running should not add a line: %q", sender.tools) }
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceToolResult, ToolCall: call, ToolResult: &sdk.ToolResult{ID: "1", Content: "boom", IsError: true}, Elapsed: 3 * time.Second})
	if len(sender.tools) != 1 || sender.tools[0] != `❌ list_directory("{\"path\":\"~/ai\"}") · 3s` { t.Fatalf("tools = %q", sender.tools) }
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceRequest})
	if len(sender.tools) != 2 || sender.tools[0] != `❌ list_directory("{\"path\":\"~/ai\"}") · 3s` || sender.tools[1] != "sending request to provider" { t.Fatalf("tools = %q", sender.tools) }
}

func TestDisplayTraceToolSuccessMarksLineInPlace(t *testing.T) {
	sender := &routingFakeSender{}
	display := Display{Sender: sender}
	call := &sdk.ToolCall{ID: "1", Name: "run_command", Arguments: "{}"}
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceToolCall, ToolCall: call, Elapsed: time.Second})
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceToolResult, ToolCall: call, ToolResult: &sdk.ToolResult{ID: "1", Content: "ok"}, Elapsed: 2 * time.Second})
	if len(sender.tools) != 1 || sender.tools[0] != `✅ run_command("{}") · 2s` { t.Fatalf("tools = %q", sender.tools) }
}

func TestDisplayTraceResponseFlushesToolTrace(t *testing.T) {
	sender := &routingFakeSender{}
	display := Display{Sender: sender}
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceResponse, Response: &sdk.Response{}})
	if sender.flushes != 1 { t.Fatalf("flushes = %d", sender.flushes) }
}
