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

func TestActorTraceRequestKeepsSameStateDuringToolContinuation(t *testing.T) {
	key := "same-turn"
	actorTraceStates = make(map[string]*actorTraceState)
	g := (*Gateway)(nil)
	g.actorTraceBeginTurn(key)
	state := actorTraceStates[key]
	state.messageID = "message-1"
	state.items = []string{"provider accepted request; processing", "tool: read_file", "✅ tool: read_file"}
	g.actorTraceBeginTurn(key)
	state = actorTraceStates[key]
	if state.messageID != "message-1" {
		t.Fatalf("message id changed during tool continuation: %q", state.messageID)
	}
	if len(state.items) != 3 || state.items[1] != "tool: read_file" {
		t.Fatalf("trace items were reset: %#v", state.items)
	}
}

func TestActorTraceRequestStartsNewStateAfterCompletedTurn(t *testing.T) {
	key := "new-turn"
	actorTraceStates = make(map[string]*actorTraceState)
	g := (*Gateway)(nil)
	g.actorTraceBeginTurn(key)
	state := actorTraceStates[key]
	state.messageID = "old-message"
	state.items = []string{"provider accepted request; processing", "✅ tool: read_file"}
	g.actorTraceComplete(key)
	g.actorTraceBeginTurn(key)
	state = actorTraceStates[key]
	if state.messageID != "" {
		t.Fatalf("completed turn reused old message: %q", state.messageID)
	}
	if len(state.items) != 0 {
		t.Fatalf("completed turn reused old items: %#v", state.items)
	}
}
