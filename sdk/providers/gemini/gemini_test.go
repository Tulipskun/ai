package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func TestBuildMapsModelAndToolResult(t *testing.T) {
	r, total, err := build(sdk.Request{Model: "test", Messages: []sdk.Turn{
		{Role: sdk.RoleModel, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "calling"}}},
		{Role: sdk.RoleToolCall, ToolCall: &sdk.ToolCall{ID: "c1", Name: "bash", Arguments: `{"command":"pwd"}`}},
		{Role: sdk.RoleToolResult, ToolResult: &sdk.ToolResult{ID: "c1", Content: "/tmp"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if r["model"] != "test" {
		t.Fatalf("model = %v, want test", r["model"])
	}
	// Only client-originated turns become input: model text and the
	// function call itself live server-side.
	input := r["input"].([]any)
	if total != 1 || len(input) != 1 {
		t.Fatalf("total=%d input=%v, want 1 function_result step", total, input)
	}
	fr := input[0].(map[string]any)
	if fr["type"] != "function_result" || fr["name"] != "bash" || fr["call_id"] != "c1" {
		t.Fatalf("unexpected function_result step: %v", fr)
	}
	parts := fr["result"].([]any)
	if parts[0].(map[string]any)["text"] != "/tmp" {
		t.Fatalf("unexpected result content: %v", parts)
	}
}

func TestBuildChainsFollowUpWithPreviousID(t *testing.T) {
	first, total, err := BuildFromOpenAI(openaiMapForTest(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := first["previous_interaction_id"]; ok {
		t.Fatal("first turn must not chain")
	}
	if total != 2 {
		t.Fatalf("total=%d, want 2 client inputs", total)
	}
	second, _, err := BuildFromOpenAI(openaiMapForTest(), total, "int_1")
	if err != nil {
		t.Fatal(err)
	}
	if second["previous_interaction_id"] != "int_1" {
		t.Fatalf("follow-up must chain: %v", second)
	}
	if len(second["input"].([]any)) != 0 {
		t.Fatalf("follow-up must send only the delta, got %v", second["input"])
	}
}

func openaiMapForTest() map[string]any {
	return map[string]any{"model": "m", "input": []any{
		map[string]any{"type": "message", "role": "user", "content": "hi"},
		map[string]any{"type": "message", "role": "assistant", "content": "calling"},
		map[string]any{"type": "function_call", "call_id": "c1", "name": "bash", "arguments": `{"command":"pwd"}`},
		map[string]any{"type": "function_call_output", "call_id": "c1", "output": "/tmp"},
	}}
}

func TestGenerateChainsInteractionID(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/interactions" {
			t.Errorf("path = %q, want /interactions", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		mu.Lock()
		n := len(bodies)
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if n == 0 {
			_, _ = w.Write([]byte(`{"id":"int_1","steps":[{"type":"function_call","id":"call_1","name":"bash","arguments":{"command":"pwd"}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"int_2","steps":[{"type":"model_output","content":[{"type":"text","text":"done"}]}]}`))
	}))
	defer srv.Close()
	c := New("k")
	c.BaseURL = srv.URL
	messages := []sdk.Turn{{Role: sdk.RoleUser, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "hi"}}}}
	resp, err := c.Generate(context.Background(), sdk.Request{Model: "m", ConversationID: "conv-1", Messages: messages})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call_1" {
		t.Fatalf("unexpected calls: %+v", resp.ToolCalls)
	}
	messages = append(messages,
		sdk.Turn{Role: sdk.RoleModel, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "calling"}}},
		sdk.Turn{Role: sdk.RoleToolCall, ToolCall: &sdk.ToolCall{ID: "call_1", Name: "bash", Arguments: `{"command":"pwd"}`}},
		sdk.Turn{Role: sdk.RoleToolResult, ToolResult: &sdk.ToolResult{ID: "call_1", Content: "/tmp"}},
	)
	resp, err = c.Generate(context.Background(), sdk.Request{Model: "m", ConversationID: "conv-1", Messages: messages})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "done" {
		t.Fatalf("unexpected content: %+v", resp.Content)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want 2", len(bodies))
	}
	if _, ok := bodies[0]["previous_interaction_id"]; ok {
		t.Fatalf("first turn must start a fresh chain: %v", bodies[0])
	}
	if bodies[1]["previous_interaction_id"] != "int_1" {
		t.Fatalf("follow-up must chain int_1: %v", bodies[1])
	}
	steps := bodies[1]["input"].([]any)
	if len(steps) != 1 || steps[0].(map[string]any)["type"] != "function_result" {
		t.Fatalf("follow-up must send only the new tool result, got %v", steps)
	}
}

func TestParseInteractionSteps(t *testing.T) {
	resp := ToInteractionResponse(interactionResponse{ID: "int_1", Steps: []interactionStep{
		{Type: "thought", Content: []interactionContent{{Type: "text", Text: "planning"}}},
		{Type: "function_call", ID: "call_1", Name: "bash", Arguments: json.RawMessage(`{"command":"pwd"}`)},
		{Type: "model_output", Content: []interactionContent{{Type: "text", Text: "done"}}},
	}}, "m")
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call_1" || resp.ToolCalls[0].Name != "bash" {
		t.Fatalf("unexpected tool calls: %+v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Arguments != `{"command":"pwd"}` {
		t.Fatalf("arguments = %q", resp.ToolCalls[0].Arguments)
	}
	if resp.Reasoning == nil || resp.Reasoning.Text != "planning" {
		t.Fatalf("unexpected reasoning: %+v", resp.Reasoning)
	}
	found := false
	for _, part := range resp.Content {
		if part.Text == "done" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing model text: %+v", resp.Content)
	}
	if resp.Provider != "gemini" {
		t.Fatalf("provider = %q", resp.Provider)
	}
}

func TestClientHeadersMergeCustom(t *testing.T) {
	c := &Client{BaseURL: "https://example.invalid", APIKey: "k"}
	got := c.WithHeaders(map[string]string{"X-Title": "ai"}).(*Client).headers()
	if got["x-goog-api-key"] != "k" || got["X-Title"] != "ai" {
		t.Fatalf("unexpected headers: %v", got)
	}
}
