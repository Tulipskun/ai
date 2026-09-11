package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func TestBuildTranslatesToolHistory(t *testing.T) {
	r := build(sdk.Request{Model: "test", Messages: []sdk.Turn{
		{Role: sdk.RoleUser, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "hi"}}},
		{Role: sdk.RoleToolCall, ToolCall: &sdk.ToolCall{ID: "c1", Name: "bash", Arguments: `{"command":"pwd"}`}},
		{Role: sdk.RoleToolResult, ToolResult: &sdk.ToolResult{ID: "c1", Content: "/tmp"}},
	}})
	input := r["input"].([]any)
	if input[1].(map[string]any)["type"] != "function_call" {
		t.Fatal("tool call was not translated")
	}
	if input[2].(map[string]any)["type"] != "function_call_output" {
		t.Fatal("tool result was not translated")
	}
}

func TestBuildReplaysResponsesReasoning(t *testing.T) {
	r := build(sdk.Request{Model: "deepseek-v4-flash", ThinkingLevel: sdk.ThinkingHigh, Messages: []sdk.Turn{
		{Role: sdk.RoleUser, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "use the tool"}}},
		{Role: sdk.RoleModel, Reasoning: &sdk.ReasoningState{ID: "rs_123", Text: "think before using the tool"}},
		{Role: sdk.RoleToolCall, ToolCall: &sdk.ToolCall{ID: "c1", Name: "bash", Arguments: `{"command":"pwd"}`}},
		{Role: sdk.RoleToolResult, ToolResult: &sdk.ToolResult{ID: "c1", Content: "/tmp"}},
	}})
	input := r["input"].([]any)
	reasoning, ok := input[1].(map[string]any)
	if !ok || reasoning["type"] != "reasoning" {
		t.Fatalf("reasoning item missing: %#v", input)
	}
	if reasoning["id"] != "rs_123" {
		t.Fatalf("reasoning id=%v", reasoning["id"])
	}
	content := reasoning["content"].([]any)
	part := content[0].(map[string]any)
	if part["type"] != "reasoning_text" || part["text"] != "think before using the tool" {
		t.Fatalf("reasoning content=%#v", part)
	}
}

func TestParseResponsePreservesReasoning(t *testing.T) {
	r := response{Model: "deepseek-v4-flash", Output: []struct {
		Type string `json:"type"`
		ID string `json:"id"`
		CallID string `json:"call_id"`
		Name string `json:"name"`
		Arguments string `json:"arguments"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}{
		{Type: "reasoning", ID: "rs_123", Content: []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "reasoning_text", Text: "think"}}},
	}}
	got := parseResponse(r)
	if got.Reasoning == nil || got.Reasoning.ID != "rs_123" || got.Reasoning.Text != "think" {
		t.Fatalf("reasoning=%#v", got.Reasoning)
	}
}

func TestGenerateFallsBackToChatCompletions(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/v1/responses" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"model \"qwen3.8-flash\" is not supported on /v1/responses; use /v1/chat/completions instead","code":"model_not_supported_on_endpoint"}}`))
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"model":"qwen3.8-flash","choices":[{"message":{"content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := New("test-key")
	client.BaseURL = server.URL + "/v1"
	client.HTTP = server.Client()
	got, err := client.Generate(context.Background(), sdk.Request{Model: "qwen3.8-flash"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if got.Model != "qwen3.8-flash" || len(got.Content) != 1 || got.Content[0].Text != "hello" {
		t.Fatalf("unexpected response: %#v", got)
	}
	if len(paths) != 2 || paths[0] != "/v1/responses" || paths[1] != "/v1/chat/completions" {
		t.Fatalf("paths=%v", paths)
	}
}

func TestClientHeadersMergeCustom(t *testing.T) {
	c := &Client{BaseURL: "https://example.invalid/v1", APIKey: "k"}
	p := c.WithHeaders(map[string]string{"X-Title": "ai", "Authorization": "Bearer hacked"})
	got := p.(*Client).headers()
	if got["Authorization"] != "Bearer k" {
		t.Fatalf("auth header must win, got %q", got["Authorization"])
	}
	if got["X-Title"] != "ai" {
		t.Fatalf("custom header missing: %v", got)
	}
}

func TestWithHeadersDeepCopies(t *testing.T) {
	c := &Client{}
	in := map[string]string{"X-Title": "ai"}
	p := c.WithHeaders(in).(*Client)
	in["X-Title"] = "mutated"
	in["X-New"] = "x"
	if p.Headers["X-Title"] != "ai" || len(p.Headers) != 1 {
		t.Fatalf("shared adapter state leaked: %v", p.Headers)
	}
	if len(c.Headers) != 0 {
		t.Fatalf("base adapter mutated: %v", c.Headers)
	}
}
