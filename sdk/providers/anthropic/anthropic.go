package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	anthro "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/Tulipskun/ai/sdk"
	"github.com/Tulipskun/ai/sdk/providers/openai"
)

type Client struct { BaseURL, APIKey, APIVersion string; Headers map[string]string; HTTP *http.Client }
func New(apiKey string) *Client { return &Client{BaseURL: "https://api.anthropic.com/v1", APIKey: apiKey, APIVersion: "2023-06-01", HTTP: http.DefaultClient} }
func (c *Client) WithAPIKey(key string) sdk.Provider { cp := *c; cp.APIKey = key; return &cp }
func (c *Client) WithBaseURL(baseURL string) sdk.Provider { cp := *c; cp.BaseURL = baseURL; return &cp }
func (c *Client) WithHeaders(headers map[string]string) sdk.Provider { cp := *c; cp.Headers = cloneHeaders(headers); return &cp }
func (c *Client) ListModels(ctx context.Context, apiKey string) ([]sdk.Model, error) {var cancel context.CancelFunc; ctx, cancel = context.WithTimeout(ctx, 30*time.Second); defer cancel(); if apiKey != "" { c = c.WithAPIKey(apiKey).(*Client) }; client := c.sdkClient(); page, err := client.Models.List(ctx, anthro.ModelListParams{}); if err != nil { return nil, mapAPIError(err) }; models := make([]sdk.Model, 0, len(page.Data)); for _, item := range page.Data { if item.ID == "" { continue }; name := item.DisplayName; if name == "" { name = item.ID }; models = append(models, sdk.Model{ID: item.ID, Name: name, SupportsTools: true, SupportsTemperature: true}) }; return models, nil }
func (c *Client) Name() string { return "anthropic" }
// build converts via the central OpenAI Responses interface:
// sdk.Request -> OpenAI canonical -> Anthropic native.
func build(req sdk.Request) map[string]any {
	return BuildFromOpenAI(openai.BuildResponsesRequest(req))
}

// BuildFromOpenAI translates a canonical OpenAI Responses request map
// (see openai.BuildResponsesRequest) into an Anthropic /messages payload.
func BuildFromOpenAI(openAIReq map[string]any) map[string]any {
	model := openai.ModelOf(openAIReq)
	maxTokens := openai.MaxOutputTokensOf(openAIReq)
	b := map[string]any{"model": model, "max_tokens": maxTokens}
	if sys := openai.InstructionsOf(openAIReq); sys != "" {
		b["system"] = sys
	}
	if temp, ok := openai.TemperatureOf(openAIReq); ok {
		b["temperature"] = temp
	}
	if effort := openai.ReasoningEffortOf(openAIReq); effort != "" && effort != string(sdk.ThinkingNone) {
		bud := map[string]int{"low": 2048, "medium": 4096, "high": 8192}[effort]
		b["thinking"] = map[string]any{"type": "enabled", "budget_tokens": bud}
		if maxTokens <= bud {
			b["max_tokens"] = bud + max(1024, maxTokens)
		}
	}
	var msgs []any
	for _, item := range openai.InputItemsOf(openAIReq) {
		m := openai.ItemMap(item)
		if m == nil {
			continue
		}
		switch m["type"] {
		case "message":
			role, _ := m["role"].(string)
			if role == "assistant" {
				// Canonical uses "assistant"; Anthropic native is also "assistant".
			} else {
				role = "user"
			}
			text, _ := m["content"].(string)
			msgs = append(msgs, map[string]any{"role": role, "content": text})
		case "reasoning":
			// Anthropic history carries thinking at request level; nothing to replay per-message.
			continue
		case "function_call":
			callID, _ := m["call_id"].(string)
			name, _ := m["name"].(string)
			argsStr, _ := m["arguments"].(string)
			var args any
			_ = json.Unmarshal([]byte(argsStr), &args)
			msgs = append(msgs, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": callID, "name": name, "input": args}}})
		case "function_call_output":
			callID, _ := m["call_id"].(string)
			output, _ := m["output"].(string)
			msgs = append(msgs, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": callID, "content": output}}})
		}
	}
	b["messages"] = msgs
	var tools []any
	for _, t := range openai.ToolsOf(openAIReq) {
		tm := openai.ItemMap(t)
		if tm == nil {
			continue
		}
		name, _ := tm["name"].(string)
		desc, _ := tm["description"].(string)
		tools = append(tools, map[string]any{"name": name, "description": desc, "input_schema": tm["parameters"]})
	}
	if len(tools) > 0 {
		b["tools"] = tools
	}
	return b
}
func max(a,b int)int{if a>b{return a};return b}
type contentBlock struct { Type, Text, ID, Name string; Input json.RawMessage `json:"input"` }
type response struct { Model string `json:"model"`; StopReason string `json:"stop_reason"`; Content []contentBlock `json:"content"`; Usage struct { Input int `json:"input_tokens"`; OutputTokens int `json:"output_tokens"`; CacheRead int `json:"cache_read_input_tokens"`; CacheCreation int `json:"cache_creation_input_tokens"` } `json:"usage"` }
func (c *Client) headers() map[string]string { h := map[string]string{"x-api-key": c.APIKey, "anthropic-version": c.APIVersion}; for k, v := range c.Headers { if v == "" || strings.EqualFold(k, "x-api-key") { continue }; h[k] = v }; return h }
func cloneHeaders(in map[string]string) map[string]string { if len(in) == 0 { return nil }; out := make(map[string]string, len(in)); for k, v := range in { out[k] = v }; return out }
func (c *Client) Generate(ctx context.Context, req sdk.Request) (sdk.Response, error) {
	client := c.sdkClient()
	msg, err := client.Messages.New(ctx, messageParams(req))
	if err != nil {
		return sdk.Response{}, mapAPIError(err)
	}
	return parseSDKMessage(msg), nil
}

// sdkClient builds an official SDK client bound to this adapter instance.
// Explicit options always win over the SDK's environment defaults
// (ANTHROPIC_API_KEY/ANTHROPIC_BASE_URL), so file-based configuration stays
// authoritative. Retries stay disabled here: the Agent owns retry policy.
func (c *Client) sdkClient() anthro.Client {
	opts := []option.RequestOption{option.WithAPIKey(c.APIKey), option.WithMaxRetries(0), option.WithHTTPClient(c.http())}
	if c.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(c.BaseURL))
	}
	for k, v := range c.Headers {
		if v == "" || strings.EqualFold(k, "x-api-key") {
			continue
		}
		opts = append(opts, option.WithHeader(k, v))
	}
	return anthro.NewClient(opts...)
}

// apiError bridges official SDK failures into the Harness error model so
// rate-limit and Retry-After handling keep working unchanged. Unlike the
// OpenAI SDK, the Anthropic error type is exported with status and response.
type apiError struct {
	status     int
	retryAfter time.Duration
	err        error
}

func (e *apiError) Error() string             { return e.err.Error() }
func (e *apiError) Unwrap() error             { return e.err }
func (e *apiError) HTTPStatusCode() int       { return e.status }
func (e *apiError) RetryAfter() time.Duration { return e.retryAfter }

func mapAPIError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *anthro.Error
	if !errors.As(err, &apiErr) {
		return err
	}
	mapped := &apiError{status: apiErr.StatusCode, err: err}
	if apiErr.Response != nil {
		mapped.retryAfter = parseRetryAfter(apiErr.Response.Header.Get("Retry-After"))
	}
	return mapped
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

func messageParams(req sdk.Request) anthro.MessageNewParams {
	params := anthro.MessageNewParams{Model: anthro.Model(req.Model), MaxTokens: int64(req.MaxOutputTokens)}
	if req.SystemPrompt != "" {
		params.System = []anthro.TextBlockParam{{Text: req.SystemPrompt}}
	}
	var messages []anthro.MessageParam
	for _, m := range req.Messages {
		switch m.Role {
		case sdk.RoleUser, sdk.RoleModel:
			text := ""
			for _, p := range m.Content {
				text += p.Text
			}
			if text == "" {
				continue
			}
			// Thinking blocks are response-only; nothing to replay per-message.
			if m.Role == sdk.RoleUser {
				messages = append(messages, anthro.NewUserMessage(anthro.NewTextBlock(text)))
			} else {
				messages = append(messages, anthro.NewAssistantMessage(anthro.NewTextBlock(text)))
			}
		case sdk.RoleToolCall:
			if m.ToolCall == nil {
				continue
			}
			var input any
			_ = json.Unmarshal([]byte(m.ToolCall.Arguments), &input)
			messages = append(messages, anthro.NewAssistantMessage(anthro.NewToolUseBlock(m.ToolCall.ID, input, m.ToolCall.Name)))
		case sdk.RoleToolResult:
			if m.ToolResult == nil {
				continue
			}
			messages = append(messages, anthro.NewUserMessage(anthro.NewToolResultBlock(m.ToolResult.ID, m.ToolResult.Content, m.ToolResult.IsError)))
		}
	}
	params.Messages = messages
	for _, t := range req.Tools {
		schema := toolSchema(t.InputSchema)
		params.Tools = append(params.Tools, anthro.ToolUnionParam{OfTool: &anthro.ToolParam{
			Name:        t.Name,
			Description: param.NewOpt(t.Description),
			InputSchema: schema,
		}})
	}
	if req.Temperature != nil {
		params.Temperature = param.NewOpt(*req.Temperature)
	}
	if effort := thinkingEffort(req); effort != "" {
		budget := map[string]int64{"low": 2048, "medium": 4096, "high": 8192}[effort]
		params.Thinking = anthro.ThinkingConfigParamUnion{OfEnabled: &anthro.ThinkingConfigEnabledParam{BudgetTokens: budget}}
		if params.MaxTokens <= budget {
			floor := params.MaxTokens
			if floor < 1024 {
				floor = 1024
			}
			params.MaxTokens = budget + floor
		}
	}
	return params
}

func thinkingEffort(req sdk.Request) string {
	effort := string(req.ThinkingLevel)
	if effort == "" || effort == string(sdk.ThinkingNone) {
		return ""
	}
	return effort
}

func toolSchema(input any) anthro.ToolInputSchemaParam {
	schema := anthro.ToolInputSchemaParam{}
	m, ok := input.(map[string]any)
	if !ok {
		return schema
	}
	if props, ok := m["properties"]; ok {
		schema.Properties = props
	}
	switch required := m["required"].(type) {
	case []any:
		for _, item := range required {
			if name, ok := item.(string); ok {
				schema.Required = append(schema.Required, name)
			}
		}
	case []string:
		schema.Required = append(schema.Required, required...)
	}
	return schema
}

func parseSDKMessage(msg *anthro.Message) sdk.Response {
	var wire response
	if msg != nil {
		wire.Model = string(msg.Model)
		wire.StopReason = string(msg.StopReason)
		wire.Usage.Input = int(msg.Usage.InputTokens)
		wire.Usage.OutputTokens = int(msg.Usage.OutputTokens)
		wire.Usage.CacheRead = int(msg.Usage.CacheReadInputTokens)
		wire.Usage.CacheCreation = int(msg.Usage.CacheCreationInputTokens)
		for _, block := range msg.Content {
			switch content := block.AsAny().(type) {
			case anthro.TextBlock:
				if content.Text != "" {
					wire.Content = append(wire.Content, contentBlock{Type: "text", Text: content.Text})
				}
			case anthro.ToolUseBlock:
				wire.Content = append(wire.Content, contentBlock{Type: "tool_use", ID: content.ID, Name: content.Name, Input: content.Input})
			}
		}
	}
	return ToOpenAIResponse(wire)
}
func (c *Client) http()*http.Client{if c.HTTP!=nil{return c.HTTP};return http.DefaultClient}

// ToOpenAIResponse converts a native Anthropic response into sdk.Response
// through the central OpenAI Responses shape.
func ToOpenAIResponse(r response) sdk.Response {
	var texts []string
	var calls []sdk.ToolCall
	for _, p := range r.Content {
		switch p.Type {
		case "text":
			texts = append(texts, p.Text)
		case "tool_use":
			calls = append(calls, sdk.ToolCall{ID: p.ID, Name: p.Name, Arguments: string(p.Input)})
		}
	}
	usage := sdk.Usage{InputTokens: r.Usage.Input, OutputTokens: r.Usage.OutputTokens, TotalTokens: r.Usage.Input + r.Usage.OutputTokens, CacheReadTokens: r.Usage.CacheRead, CacheWriteTokens: r.Usage.CacheCreation}
	out := openai.ResponsesResponseFromParts(r.Model, r.StopReason, texts, calls, nil, usage)
	out.Provider = "anthropic"
	out.Cache = sdk.CacheInfo{Layer: "provider", Hit: usage.CacheReadTokens > 0}
	return out
}
