package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"os"

	"github.com/Tulipskun/ai/sdk"
	"github.com/Tulipskun/ai/sdk/providers/internal"
)

type Client struct { BaseURL, APIKey, APIVersion string; HTTP *http.Client }
func New(apiKey string) *Client { if apiKey == "" { apiKey = os.Getenv("ANTHROPIC_API_KEY") }; return &Client{BaseURL: "https://api.anthropic.com/v1", APIKey: apiKey, APIVersion: "2023-06-01", HTTP: http.DefaultClient} }
func (c *Client) WithAPIKey(key string) sdk.Provider { cp := *c; cp.APIKey = key; return &cp }
func (c *Client) WithBaseURL(baseURL string) sdk.Provider { cp := *c; cp.BaseURL = baseURL; return &cp }
func (c *Client) ListModels(ctx context.Context, apiKey string) ([]sdk.Model, error) { if apiKey != "" { c = c.WithAPIKey(apiKey).(*Client) }; var r struct { Data []struct { ID string `json:"id"`; DisplayName string `json:"display_name"` } `json:"data"` }; if err := internal.DoJSON(ctx, c.http(), http.MethodGet, c.BaseURL+"/models", c.headers(), nil, &r); err != nil { return nil, err }; models := make([]sdk.Model, 0, len(r.Data)); for _, item := range r.Data { if item.ID == "" { continue }; name := item.DisplayName; if name == "" { name = item.ID }; models = append(models, sdk.Model{ID: item.ID, Name: name, SupportsStreaming: true, SupportsTools: true, SupportsTemperature: true}) }; return models, nil }
func (c *Client) Name() string { return "anthropic" }
func build(req sdk.Request) map[string]any { b := map[string]any{"model": req.Model, "max_tokens": req.MaxOutputTokens}; if req.SystemPrompt != "" { b["system"] = req.SystemPrompt }; if req.Temperature != nil { b["temperature"] = *req.Temperature }; if req.ThinkingLevel != "" && req.ThinkingLevel != sdk.ThinkingNone { bud := map[sdk.ThinkingLevel]int{sdk.ThinkingLow: 2048, sdk.ThinkingMedium: 4096, sdk.ThinkingHigh: 8192}[req.ThinkingLevel]; b["thinking"] = map[string]any{"type": "enabled", "budget_tokens": bud}; if req.MaxOutputTokens <= bud { b["max_tokens"] = bud + max(1024, req.MaxOutputTokens) } }; var msgs []any; for _, m := range req.Messages { switch m.Role { case sdk.RoleUser, sdk.RoleModel: text := ""; for _, p := range m.Content { text += p.Text }; role := string(m.Role); if m.Role == sdk.RoleModel { role = "assistant" }; msgs = append(msgs, map[string]any{"role": role, "content": text}); case sdk.RoleToolCall: if m.ToolCall != nil { var args any; _ = json.Unmarshal([]byte(m.ToolCall.Arguments), &args); msgs = append(msgs, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": m.ToolCall.ID, "name": m.ToolCall.Name, "input": args}}}) }; case sdk.RoleToolResult: if m.ToolResult != nil { msgs = append(msgs, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": m.ToolResult.ID, "content": m.ToolResult.Content}}}) } } }; b["messages"] = msgs; if len(req.Tools) > 0 { ts := make([]any, 0, len(req.Tools)); for _, t := range req.Tools { ts = append(ts, map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.InputSchema}) }; b["tools"] = ts }; if req.Stream { b["stream"] = true }; return b }
func max(a, b int) int { if a > b { return a }; return b }
type response struct { Model string `json:"model"`; StopReason string `json:"stop_reason"`; Content []struct { Type, Text, ID, Name string; Input json.RawMessage `json:"input"` } `json:"content"`; Usage struct { Input int `json:"input_tokens"`; OutputTokens int `json:"output_tokens"`; CacheRead int `json:"cache_read_input_tokens"`; CacheCreation int `json:"cache_creation_input_tokens"` } `json:"usage"` }
func (c *Client) headers() map[string]string { return map[string]string{"x-api-key": c.APIKey, "anthropic-version": c.APIVersion} }
func (c *Client) Generate(ctx context.Context, req sdk.Request) (sdk.Response, error) { var r response; err := internal.DoJSON(ctx, c.http(), http.MethodPost, c.BaseURL+"/messages", c.headers(), build(req), &r); if err != nil { return sdk.Response{}, err }; out := sdk.Response{Provider: "anthropic", Model: r.Model, FinishReason: r.StopReason, Usage: sdk.Usage{InputTokens: r.Usage.Input, OutputTokens: r.Usage.OutputTokens, TotalTokens: r.Usage.Input + r.Usage.OutputTokens, CacheReadTokens: r.Usage.CacheRead, CacheWriteTokens: r.Usage.CacheCreation}, Cache: sdk.CacheInfo{Layer: "provider"}}; out.Cache.Hit = out.Usage.CacheReadTokens > 0; for _, p := range r.Content { switch p.Type { case "text": out.Content = append(out.Content, sdk.ContentPart{Type: sdk.ContentText, Text: p.Text}); case "tool_use": out.ToolCalls = append(out.ToolCalls, sdk.ToolCall{ID: p.ID, Name: p.Name, Arguments: string(p.Input)}) } }; return out, nil }

func (c *Client) Stream(ctx context.Context, req sdk.Request) (<-chan sdk.Event, error) {
	req.Stream = true
	ch := make(chan sdk.Event, 16)
	go func() {
		defer close(ch)
		type toolState struct { id, name string; arguments string }
		tools := map[string]*toolState{}
		var reasoningID string
		err := internal.SSE(ctx, c.http(), http.MethodPost, c.BaseURL+"/messages", c.headers(), build(req), func(data []byte) error {
			var e struct {
				Type string `json:"type"`
				Index int `json:"index"`
				Delta struct { Type string `json:"type"`; Text string `json:"text"`; Thinking string `json:"thinking"`; PartialJSON string `json:"partial_json"`; Signature string `json:"signature"` } `json:"delta"`
				ContentBlock struct { Type string `json:"type"`; ID string `json:"id"`; Name string `json:"name"`; Thinking string `json:"thinking"` } `json:"content_block"`
			}
			if err := json.Unmarshal(data, &e); err != nil { return nil }
			switch e.Type {
			case "content_block_start":
				switch e.ContentBlock.Type {
				case "tool_use":
					tools[e.ContentBlock.ID] = &toolState{id: e.ContentBlock.ID, name: e.ContentBlock.Name}
				case "thinking":
					reasoningID = e.ContentBlock.ID
					if e.ContentBlock.Thinking != "" { ch <- sdk.Event{Type: sdk.EventReasoning, Reasoning: &sdk.ReasoningState{ID: reasoningID, Text: e.ContentBlock.Thinking}} }
				}
			case "content_block_delta":
				switch e.Delta.Type {
				case "text_delta":
					if e.Delta.Text != "" { ch <- sdk.Event{Type: sdk.EventText, Text: e.Delta.Text} }
				case "thinking_delta":
					if e.Delta.Thinking != "" { ch <- sdk.Event{Type: sdk.EventReasoning, Reasoning: &sdk.ReasoningState{ID: reasoningID, Text: e.Delta.Thinking}} }
				case "input_json_delta":
					for _, state := range tools { state.arguments += e.Delta.PartialJSON }
				}
			case "content_block_stop":
				for _, state := range tools { if state.arguments != "" { ch <- sdk.Event{Type: sdk.EventToolCall, ToolCall: &sdk.ToolCall{ID: state.id, Name: state.name, Arguments: state.arguments}}; delete(tools, state.id); break } }
			case "message_stop":
				ch <- sdk.Event{Type: sdk.EventDone}
			}
			return nil
		})
		if err != nil { ch <- sdk.Event{Type: sdk.EventError, Err: err} }
	}()
	return ch, nil
}
func (c *Client) http() *http.Client { if c.HTTP != nil { return c.HTTP }; return http.DefaultClient }
