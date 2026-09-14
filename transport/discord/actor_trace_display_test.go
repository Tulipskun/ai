package discord

import (
	"strings"
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func TestFormatActorToolCallShowsToolNameWithoutArguments(t *testing.T) {
	call := &sdk.ToolCall{Name: "read_file", Arguments: `{"path":"/secret/file.txt"}`}
	got := formatActorToolCall(call)
	if got != "tool: read_file" {
		t.Fatalf("tool label=%q", got)
	}
	if strings.Contains(got, "secret") || strings.Contains(got, "Arguments") || strings.Contains(got, "path") {
		t.Fatalf("tool arguments leaked: %q", got)
	}
}

func TestFormatActorToolResultKeepsToolName(t *testing.T) {
	call := &sdk.ToolCall{Name: "browser_open"}
	got := formatActorToolResult(call, &sdk.ToolResult{ID: "call-1"})
	if got != "✅ tool: browser_open" {
		t.Fatalf("tool result=%q", got)
	}
}
