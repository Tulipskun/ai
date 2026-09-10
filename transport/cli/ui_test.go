package cli

import (
	"bytes"
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func TestUIStreamsResponseAndTools(t *testing.T) {
	var out bytes.Buffer
	ui := NewUI(&out)
	ui.Trace(sdk.TraceEvent{Stage: sdk.TraceResponseText, Text: "hello "})
	ui.Trace(sdk.TraceEvent{Stage: sdk.TraceResponseText, Text: "world"})
	ui.EndTurn()
	call := sdk.ToolCall{Name: "read_file", Arguments: `{"path":"x"}`}
	ui.Trace(sdk.TraceEvent{Stage: sdk.TraceToolCall, ToolCall: &call})
	result := sdk.ToolResult{Content: "ok"}
	ui.Trace(sdk.TraceEvent{Stage: sdk.TraceToolResult, ToolResult: &result})
	got := out.String()
	want := "ai › hello world\ntool › read_file {\"path\":\"x\"}\ntool › ok · ok\n"
	if got != want { t.Fatalf("rendered UI = %q, want %q", got, want) }
}

func TestUIHidesToolTrace(t *testing.T) {
	var out bytes.Buffer
	ui := NewUI(&out)
	ui.ShowToolTrace = false
	call := sdk.ToolCall{Name: "exec_command", Arguments: `{"command":"echo hi"}`}
	ui.Trace(sdk.TraceEvent{Stage: sdk.TraceToolCall, ToolCall: &call})
	if out.Len() != 0 { t.Fatalf("tool trace should be hidden, got %q", out.String()) }
}
