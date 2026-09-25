// Package opencode speaks the OpenAI-compatible wire protocol with the
// request fingerprint of the real opencode client. Like opencode itself it
// tries /responses first and falls back to /chat/completions when the
// endpoint cannot serve the model.
//
// Observed in opencode 1.18.31: for providers whose id starts with
// "opencode" every model request carries x-opencode-session (the opencode
// session id, e.g. ses_f...), x-opencode-request, x-opencode-client and a
// User-Agent of the form opencode/<version>. The Zen free tier additionally
// behaves like an OpenRouter-style gateway: it rejects requests without a
// session id (MissingSessionID) and rate-limits requests whose User-Agent is
// not opencode (FreeUsageLimitError), so both headers are required for free
// models. HTTP-Referer/X-Title mirror what opencode sends to other
// OpenAI-compatible gateways (openrouter/kilo/llmgateway/nvidia transforms).
//
// Only the session id and the gateway attribution headers are reproduced
// here. Internal opencode ids (request user id, client flags, project id)
// are deliberately not forged: the free tier works without them.
package opencode

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Tulipskun/ai/sdk"
	"github.com/Tulipskun/ai/sdk/providers/internal"
	"github.com/Tulipskun/ai/sdk/providers/openai"
)

const (
	DefaultBaseURL = "https://opencode.ai/zen/v1"
	Referer        = "https://opencode.ai/"
	Title          = "opencode"

	// The free tier only answers a request that looks like it came from the
	// OpenCode client: its User-Agent carries the client version plus the
	// ai-sdk and runtime tags, and it sends the x-opencode-* session headers.
	// Verified against the real client (captured request, replayed unchanged).
	DefaultUserAgent    = "opencode/1.18.32 ai-sdk/provider-utils/4.0.45 runtime/bun/1.3.14"
	DefaultClientName   = "cli"
	DefaultProjectLabel = "ai"

	headerSession = "x-opencode-session"
	headerClient  = "x-opencode-client"
	headerProject = "x-opencode-project"
	headerRequest = "x-opencode-request"
	headerReferer = "HTTP-Referer"
	headerTitle   = "X-Title"
	headerUA      = "User-Agent"
)

const sessionAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// minted keeps one opencode-style session id per harness session for the
// lifetime of the process, so all turns of a session attribute to the same
// provider-facing id (opencode itself mints one id per session).
var minted sync.Map // map[string]string

// SessionIDFor returns the stable opencode-style session id for a harness
// session key, minting it on first use. An empty key mints a fresh id on
// every call and is never cached.
func SessionIDFor(key string) string {
	if key == "" {
		return mint()
	}
	if v, ok := minted.Load(key); ok {
		if id, ok := v.(string); ok && id != "" {
			return id
		}
	}
	id := mint()
	if actual, loaded := minted.LoadOrStore(key, id); loaded {
		if prev, ok := actual.(string); ok && prev != "" {
			return prev
		}
	}
	return id
}

// mint builds ses_f<8 hex>ffe<14 alnum>: the 8 hex digits descend with wall
// time (like opencode's own ids, which sort newest-first) and the tail is
// 14 crypto-random alphanumerics.
func mint() string {
	prefix := uint32(0xffffffff) - uint32(time.Now().Unix())
	var tail [14]byte
	var buf [14]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Fall back to time-nanosecond mixing; ids stay well-formed.
		ns := time.Now().UnixNano()
		for i := range buf {
			buf[i] = byte(ns >> (8 * (i % 8)))
			ns = ns*6364136223846793005 + 1442695040888963407
		}
	}
	for i, b := range buf {
		tail[i] = sessionAlphabet[int(b)%len(sessionAlphabet)]
	}
	var sb strings.Builder
	sb.Grow(30)
	sb.WriteString("ses_f")
	const hexd = "0123456789abcdef"
	for shift := 28; shift >= 0; shift -= 4 {
		sb.WriteByte(hexd[(prefix>>uint(shift))&0xf])
	}
	sb.WriteString("ffe")
	sb.Write(tail[:])
	return sb.String()
}

type Client struct {
	BaseURL string
	APIKey  string
	Headers map[string]string
	HTTP    *http.Client
}

func New(apiKey string) *Client {
	return &Client{BaseURL: DefaultBaseURL, APIKey: apiKey, HTTP: http.DefaultClient}
}
func (c *Client) WithAPIKey(key string) sdk.Provider      { cp := *c; cp.APIKey = key; return &cp }
func (c *Client) WithBaseURL(baseURL string) sdk.Provider { cp := *c; cp.BaseURL = baseURL; return &cp }
func (c *Client) WithHeaders(headers map[string]string) sdk.Provider {
	cp := *c
	cp.Headers = cloneHeaders(headers)
	return &cp
}
func (c *Client) Name() string { return "opencode" }

func (c *Client) sessionID(req sdk.Request) string { return SessionIDFor(req.SessionID) }

// project labels the caller the way the client does, so a request that arrives
// with no configured project still carries the header.
func (c *Client) project() string {
	for k, v := range c.Headers {
		if strings.EqualFold(k, headerProject) && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return DefaultProjectLabel
}

// headers merges explicit config headers over the opencode fingerprint,
// except the session header which always stays adapter-computed so every
// turn of a harness session attributes to the same provider-facing id.
func (c *Client) headers(sessionID string) map[string]string {
	h := map[string]string{
		"Authorization": "Bearer " + c.APIKey,
		headerUA:        DefaultUserAgent,
		headerReferer:   Referer,
		headerTitle:     Title,
		headerClient:    DefaultClientName,
		headerProject:   c.project(),
		headerRequest:   sessionID,
	}
	for k, v := range c.Headers {
		if v == "" || strings.EqualFold(k, "Authorization") || strings.EqualFold(k, headerSession) {
			continue
		}
		h[k] = v
	}
	h[headerSession] = sessionID
	return h
}

func (c *Client) ListModels(ctx context.Context, apiKey string) ([]sdk.Model, error) {
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if apiKey != "" {
		c = c.WithAPIKey(apiKey).(*Client)
	}
	var r struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := internal.DoJSON(ctx, c.http(), http.MethodGet, c.BaseURL+"/models", c.headers(SessionIDFor("")), nil, &r); err != nil {
		return nil, err
	}
	models := make([]sdk.Model, 0, len(r.Data))
	for _, item := range r.Data {
		if item.ID == "" {
			continue
		}
		models = append(models, sdk.Model{ID: item.ID, Name: item.ID, SupportsStreaming: true, SupportsTemperature: true})
	}
	return models, nil
}

// buildChatRequest mirrors what the OpenCode client sends to
// /chat/completions: the same messages and tools plus tool_choice, the token
// budget and stream_options. Zen's free tier rejects a request that is missing
// the client's shape, so the extras are not decoration.
func buildChatRequest(req sdk.Request) map[string]any {
	b := openai.BuildChatRequest(req)
	if len(req.Tools) > 0 {
		b["tool_choice"] = "auto"
	}
	if req.MaxOutputTokens > 0 {
		b["max_tokens"] = req.MaxOutputTokens
	}
	if req.Stream {
		b["stream_options"] = map[string]any{"include_usage": true}
	}
	return b
}

// buildResponsesRequest mirrors openai.BuildResponsesRequest but carries the
// system prompt as the leading developer input item instead of the
// "instructions" field: Zen rejects large instructions payloads, while the
// real opencode client sends its system prompt as input items.
func buildResponsesRequest(req sdk.Request) map[string]any {
	b := openai.BuildResponsesRequest(req)
	sys, _ := b["instructions"].(string)
	delete(b, "instructions")
	if sys == "" {
		return b
	}
	input, _ := b["input"].([]any)
	b["input"] = append([]any{map[string]any{"type": "message", "role": "developer", "content": sys}}, input...)
	return b
}

func (c *Client) Generate(ctx context.Context, req sdk.Request) (sdk.Response, error) {
	sid := c.sessionID(req)
	var r openai.ResponsesResponse
	if err := internal.DoJSON(ctx, c.http(), http.MethodPost, c.BaseURL+"/responses", c.headers(sid), buildResponsesRequest(req), &r); err == nil {
		return openai.ParseResponsesResponse(r), nil
	} else if !shouldTryChat(err) {
		return sdk.Response{}, err
	}
	var chat openai.ChatResponse
	if chatErr := internal.DoJSON(ctx, c.http(), http.MethodPost, c.BaseURL+"/chat/completions", c.headers(sid), buildChatRequest(req), &chat); chatErr != nil {
		return sdk.Response{}, chatErr
	}
	return openai.ParseChatResponse(chat), nil
}

// shouldTryChat reports whether a /responses failure means "not this endpoint",
// so the sibling /chat/completions is worth one attempt. Zen serves some models
// only on /responses and others only on /chat/completions, and its answers for
// the wrong endpoint are not uniform: 404, 500, 503 ("Endpoint is unavailable")
// and, for the free tier, a 403 FreeTierError. 401/429 never fall back: those
// are the same verdict on either endpoint.
func shouldTryChat(err error) bool {
	var httpErr *internal.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	switch httpErr.StatusCode {
	case http.StatusNotFound:
		return true
	case http.StatusForbidden:
		return strings.Contains(httpErr.Body, "FreeTierError")
	case http.StatusBadRequest:
		return strings.Contains(httpErr.Body, `"code":"model_not_supported_on_endpoint"`)
	case http.StatusInternalServerError, http.StatusServiceUnavailable:
		return true
	}
	return false
}

func (c *Client) Stream(ctx context.Context, req sdk.Request) (<-chan sdk.Event, error) {
	req.Stream = true
	ch := make(chan sdk.Event, 16)
	go func() {
		defer close(ch)
		sid := c.sessionID(req)
		emitted := false
		if err := c.streamResponses(ctx, req, sid, ch, &emitted); err == nil {
			return
		} else if emitted || !shouldTryChat(err) {
			ch <- sdk.Event{Type: sdk.EventError, Err: err}
			return
		}
		if err := c.streamChat(ctx, req, sid, ch, &emitted); err != nil {
			ch <- sdk.Event{Type: sdk.EventError, Err: err}
		}
	}()
	return ch, nil
}

func (c *Client) streamResponses(ctx context.Context, req sdk.Request, sid string, ch chan<- sdk.Event, emitted *bool) error {
	mark := func() {
		if emitted != nil {
			*emitted = true
		}
	}
	return internal.SSE(ctx, c.http(), http.MethodPost, c.BaseURL+"/responses", c.headers(sid), buildResponsesRequest(req), func(data []byte) error {
		var e struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
			Item  struct {
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"item"`
			Response struct {
				Model  string `json:"model"`
				Status string `json:"status"`
				Usage  struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
					TotalTokens  int `json:"total_tokens"`
				} `json:"usage"`
			} `json:"response"`
		}
		if json.Unmarshal(data, &e) != nil {
			return nil
		}
		switch e.Type {
		case "response.output_text.delta":
			if e.Delta != "" {
				mark()
				ch <- sdk.Event{Type: sdk.EventText, Text: e.Delta}
			}
		case "response.reasoning_summary_text.delta":
			if e.Delta != "" {
				mark()
				ch <- sdk.Event{Type: sdk.EventReasoning, Reasoning: &sdk.ReasoningState{Text: e.Delta}}
			}
		case "response.function_call_arguments.done":
			if e.Item.CallID != "" {
				mark()
				ch <- sdk.Event{Type: sdk.EventToolCall, ToolCall: &sdk.ToolCall{ID: e.Item.CallID, Name: e.Item.Name, Arguments: e.Item.Arguments}}
			}
		case "response.completed":
			mark()
			// The closing event carries the token counts, so a streamed turn
			// reports the same usage a non-streamed one does.
			ch <- sdk.Event{Type: sdk.EventDone, Response: &sdk.Response{
				Model:        e.Response.Model,
				FinishReason: e.Response.Status,
				Usage: sdk.Usage{
					InputTokens:  e.Response.Usage.InputTokens,
					OutputTokens: e.Response.Usage.OutputTokens,
					TotalTokens:  e.Response.Usage.TotalTokens,
				},
			}}
		}
		return nil
	})
}

func (c *Client) streamChat(ctx context.Context, req sdk.Request, sid string, ch chan<- sdk.Event, emitted *bool) error {
	type toolState struct {
		id, name, args string
	}
	tools := map[int]*toolState{}
	// Zen reports the token counts in a trailing chunk that carries only
	// `usage`, after the closing choice. The closing `done` goes out first (the
	// turn may continue with tool calls), and the real counts follow on a second
	// `done`, which is the one the agent keeps.
	var usage sdk.Usage
	doneSent := false
	mark := func() {
		if emitted != nil {
			*emitted = true
		}
	}
	err := internal.SSE(ctx, c.http(), http.MethodPost, c.BaseURL+"/chat/completions", c.headers(sid), buildChatRequest(req), func(data []byte) error {
		var e struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(data, &e) != nil {
			return nil
		}
		if e.Usage != nil {
			usage = sdk.Usage{
				InputTokens:  e.Usage.PromptTokens,
				OutputTokens: e.Usage.CompletionTokens,
				TotalTokens:  e.Usage.TotalTokens,
			}
		}
		for _, choice := range e.Choices {
			if choice.Delta.Content != "" {
				mark()
				ch <- sdk.Event{Type: sdk.EventText, Text: choice.Delta.Content}
			}
			for _, call := range choice.Delta.ToolCalls {
				st := tools[call.Index]
				if st == nil {
					st = &toolState{}
					tools[call.Index] = st
				}
				if call.ID != "" {
					st.id = call.ID
				}
				if call.Function.Name != "" {
					st.name = call.Function.Name
				}
				st.args += call.Function.Arguments
			}
			if choice.FinishReason == "tool_calls" {
				for _, st := range tools {
					if st.name != "" {
						mark()
						ch <- sdk.Event{Type: sdk.EventToolCall, ToolCall: &sdk.ToolCall{ID: st.id, Name: st.name, Arguments: st.args}}
					}
				}
				tools = map[int]*toolState{}
			}
			if choice.FinishReason != "" {
				doneSent = true
				mark()
				ch <- sdk.Event{Type: sdk.EventDone}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// The token counts ride out on their own `done` so a turn that continues
	// with tool calls is not closed twice for the same stream.
	if usage != (sdk.Usage{}) {
		mark()
		ch <- sdk.Event{Type: sdk.EventDone, Response: &sdk.Response{Usage: usage}}
	} else if !doneSent {
		mark()
		ch <- sdk.Event{Type: sdk.EventDone}
	}
	return nil
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func cloneHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
