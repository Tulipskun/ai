package anthropic

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tulipskun/ai/sdk"
)

func TestBuildUsesAnthropicAssistantRole(t *testing.T) {
	r:=build(sdk.Request{Model:"test",MaxOutputTokens:1000,Messages:[]sdk.Turn{{Role:sdk.RoleModel,Content:[]sdk.ContentPart{{Type:sdk.ContentText,Text:"hello"}}}}})
	msgs:=r["messages"].([]any)
	if msgs[0].(map[string]any)["role"]!="assistant"{t.Fatal("model role must map to anthropic assistant")}
}

func TestClientHeadersMergeCustom(t *testing.T) {
	c := &Client{BaseURL: "https://example.invalid", APIKey: "k", APIVersion: "2023-06-01"}
	got := c.WithHeaders(map[string]string{"X-Title": "ai"}).(*Client).headers()
	if got["x-api-key"] != "k" || got["anthropic-version"] != "2023-06-01" || got["X-Title"] != "ai" {
		t.Fatalf("unexpected headers: %v", got)
	}
}

func TestGenerateIgnoresSDKEnvironmentDefaults(t *testing.T) {
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","model":"m","stop_reason":"end_turn","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()
	t.Setenv("ANTHROPIC_API_KEY", "env-key-must-lose")
	t.Setenv("ANTHROPIC_BASE_URL", "https://env.invalid/v9")
	c := &Client{BaseURL: server.URL, APIKey: "file-key", APIVersion: "2023-06-01", HTTP: server.Client()}
	if _, err := c.Generate(context.Background(), sdk.Request{Model: "m", MaxOutputTokens: 16}); err != nil {
		t.Fatal(err)
	}
	if auth != "file-key" {
		t.Fatalf("explicit config must win over env, got %q", auth)
	}
}

func TestGenerateMapsRateLimitWithRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
	}))
	defer server.Close()
	c := &Client{BaseURL: server.URL, APIKey: "k", APIVersion: "2023-06-01", HTTP: server.Client()}
	_, err := c.Generate(context.Background(), sdk.Request{Model: "m", MaxOutputTokens: 16})
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	var statusErr sdk.HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.HTTPStatusCode() != 429 {
		t.Fatalf("status not preserved: %v", err)
	}
	var retryErr sdk.RetryAfterError
	if !errors.As(err, &retryErr) || retryErr.RetryAfter() != 7*time.Second {
		t.Fatalf("retry-after not preserved: %v", err)
	}
}

func TestMessageParamsShapesHistory(t *testing.T) {
	temp := 0.7
	params := messageParams(sdk.Request{Model: "m", SystemPrompt: "sys", Temperature: &temp, ThinkingLevel: sdk.ThinkingLow,
		MaxOutputTokens: 100,
		Messages: []sdk.Turn{
			{Role: sdk.RoleUser, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "hi"}}},
			{Role: sdk.RoleToolCall, ToolCall: &sdk.ToolCall{ID: "c1", Name: "bash", Arguments: `{"command":"pwd"}`}},
			{Role: sdk.RoleToolResult, ToolResult: &sdk.ToolResult{ID: "c1", Content: "/tmp"}},
		},
		Tools: []sdk.Tool{{Name: "bash", Description: "run", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}, "required": []string{"command"}}}},
	})
	if string(params.Model) != "m" {
		t.Fatalf("model = %q", params.Model)
	}
	if len(params.Messages) != 3 {
		t.Fatalf("messages = %d, want user+assistant(tool_use)+user(result)", len(params.Messages))
	}
	if len(params.Tools) != 1 || params.Tools[0].OfTool == nil || params.Tools[0].OfTool.Name != "bash" {
		t.Fatalf("unexpected tools: %+v", params.Tools)
	}
	schema := params.Tools[0].OfTool.InputSchema
	if _, ok := schema.Properties.(map[string]any); !ok {
		t.Fatalf("schema properties missing: %+v", schema)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "command" {
		t.Fatalf("schema required = %v", schema.Required)
	}
}
