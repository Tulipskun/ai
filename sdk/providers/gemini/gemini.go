package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Tulipskun/ai/sdk"
	"github.com/Tulipskun/ai/sdk/providers/internal"
	"github.com/Tulipskun/ai/sdk/providers/openai"
)

type Client struct {
	BaseURL string
	APIKey  string
	Headers map[string]string
	HTTP    *http.Client
	// chains tracks one server-side interaction chain per conversation so
	// follow-up turns continue with previous_interaction_id instead of
	// replaying history. Shared across With* clones via pointer.
	chains *interactionChainStore
}

type interactionChain struct {
	lastID           string
	sentClientInputs int
}

type interactionChainStore struct {
	mu     sync.Mutex
	chains map[string]*interactionChain
}

func newChainStore() *interactionChainStore {
	return &interactionChainStore{chains: make(map[string]*interactionChain)}
}

func (s *interactionChainStore) get(id string) *interactionChain {
	if s == nil || id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.chains[id]
	if st == nil {
		return nil
	}
	cp := *st
	return &cp
}

func (s *interactionChainStore) set(id string, st *interactionChain) {
	if s == nil || id == "" || st == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *st
	s.chains[id] = &cp
}

func New(apiKey string) *Client { return &Client{BaseURL: "https://generativelanguage.googleapis.com/v1beta", APIKey: apiKey, HTTP: http.DefaultClient, chains: newChainStore()} }
func (c *Client) WithAPIKey(key string) sdk.Provider { cp := *c; cp.APIKey = key; return &cp }
func (c *Client) WithBaseURL(baseURL string) sdk.Provider { cp := *c; cp.BaseURL = baseURL; return &cp }
func (c *Client) WithHeaders(headers map[string]string) sdk.Provider { cp := *c; cp.Headers = cloneHeaders(headers); return &cp }
func (c *Client) ListModels(ctx context.Context, apiKey string) ([]sdk.Model, error) {var cancel context.CancelFunc; ctx, cancel = context.WithTimeout(ctx, 30*time.Second); defer cancel(); key := apiKey; if key == "" { key = c.APIKey }; u := c.BaseURL + "/models"; if key != "" { u += "?key=" + url.QueryEscape(key) }; var r struct { Models []struct { Name string `json:"name"`; DisplayName string `json:"displayName"`; Supported []string `json:"supportedGenerationMethods"` } `json:"models"` }; if err := internal.DoJSON(ctx, c.http(), http.MethodGet, u, nil, nil, &r); err != nil { return nil, err }; models := make([]sdk.Model, 0, len(r.Models)); for _, item := range r.Models { id := strings.TrimPrefix(item.Name, "models/"); if id == "" { continue }; name := item.DisplayName; if name == "" { name = id }; models = append(models, sdk.Model{ID: id, Name: name, SupportsTools: true, SupportsThinking: true, SupportsTemperature: true}) }; return models, nil }
func (c *Client) Name() string { return "gemini" }
// build converts via the central OpenAI Responses interface:
// sdk.Request -> OpenAI canonical -> Gemini Interactions native.
// It sends the full client-originated history; Generate narrows it to the
// delta for chained follow-up turns.
func build(req sdk.Request) (map[string]any, int, error) {
	return BuildFromOpenAI(openai.BuildResponsesRequest(req), 0, "")
}

// BuildFromOpenAI translates a canonical OpenAI Responses request map
// (see openai.BuildResponsesRequest) into a Gemini Interactions payload.
// Only client-originated items (user messages and function outputs) become
// input steps: model outputs and function calls already live server-side
// once a chain exists. skipClientInputs drops that many leading client
// inputs for chained follow-ups; it returns the payload plus the total
// client input count so callers can chain the next turn. prevID chains the
// request with previous_interaction_id; empty starts a fresh interaction.
func BuildFromOpenAI(openAIReq map[string]any, skipClientInputs int, prevID string) (map[string]any, int, error) {
	b := map[string]any{"model": openai.ModelOf(openAIReq)}
	if sys := openai.InstructionsOf(openAIReq); sys != "" {
		b["system_instruction"] = sys
	}
	callNames := map[string]string{}
	for _, item := range openai.InputItemsOf(openAIReq) {
		if m := openai.ItemMap(item); m != nil && m["type"] == "function_call" {
			if id, _ := m["call_id"].(string); id != "" {
				callNames[id], _ = m["name"].(string)
			}
		}
	}
	var input []any
	clientInputs := 0
	for _, item := range openai.InputItemsOf(openAIReq) {
		m := openai.ItemMap(item)
		if m == nil {
			continue
		}
		var step map[string]any
		switch m["type"] {
		case "message":
			role, _ := m["role"].(string)
			if role != "user" {
				// Assistant messages live server-side once chained.
				continue
			}
			text, _ := m["content"].(string)
			step = map[string]any{"type": "user_input", "content": []any{map[string]any{"type": "text", "text": text}}}
		case "reasoning", "function_call":
			// Server-side steps; never replayed.
			continue
		case "function_call_output":
			callID, _ := m["call_id"].(string)
			output, _ := m["output"].(string)
			name := callNames[callID]
			if name == "" {
				name = callID
			}
			step = map[string]any{"type": "function_result", "name": name, "call_id": callID, "result": []any{map[string]any{"type": "text", "text": output}}}
		default:
			continue
		}
		if clientInputs < skipClientInputs {
			clientInputs++
			continue
		}
		clientInputs++
		input = append(input, step)
	}
	if input == nil {
		input = []any{}
	}
	b["input"] = input
	cfg := map[string]any{}
	if temp, ok := openai.TemperatureOf(openAIReq); ok {
		cfg["temperature"] = temp
	}
	if maxTokens := openai.MaxOutputTokensOf(openAIReq); maxTokens > 0 {
		cfg["max_output_tokens"] = maxTokens
	}
	if effort := openai.ReasoningEffortOf(openAIReq); effort != "" && effort != string(sdk.ThinkingNone) {
		cfg["thinking_level"] = effort
	}
	if len(cfg) > 0 {
		b["generation_config"] = cfg
	}
	tools := openai.ToolsOf(openAIReq)
	if len(tools) > 0 {
		b["tools"] = tools
	}
	// store:true keeps the interaction server-side so follow-up turns can
	// chain with previous_interaction_id. The Harness still owns session
	// state in its own session database.
	b["store"] = true
	if prevID != "" {
		b["previous_interaction_id"] = prevID
	}
	return b, clientInputs, nil
}
type interactionContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type interactionStep struct {
	Type      string               `json:"type"`
	ID        string               `json:"id"`
	Name      string               `json:"name"`
	Arguments json.RawMessage      `json:"arguments"`
	Content   []interactionContent `json:"content"`
}

type interactionResponse struct {
	ID    string            `json:"id"`
	Steps []interactionStep `json:"steps"`
}

func (c *Client) endpoint() string { return c.BaseURL + "/interactions" }

func (c *Client) Generate(ctx context.Context, req sdk.Request) (sdk.Response, error) {
	canonical := openai.BuildResponsesRequest(req)
	prevID, skip := "", 0
	if st := c.chains.get(req.ConversationID); st != nil {
		prevID, skip = st.lastID, st.sentClientInputs
	}
	body, total, err := BuildFromOpenAI(canonical, skip, prevID)
	if err != nil {
		return sdk.Response{}, err
	}
	if total <= skip {
		// History shrank or carried nothing new (repair path):
		// start a fresh chain with the full client history.
		if body, total, err = BuildFromOpenAI(canonical, 0, ""); err != nil {
			return sdk.Response{}, err
		}
	}
	var r interactionResponse
	if err := internal.DoJSON(ctx, c.http(), http.MethodPost, c.endpoint(), c.headers(), body, &r); err != nil {
		return sdk.Response{}, err
	}
	c.chains.set(req.ConversationID, &interactionChain{lastID: r.ID, sentClientInputs: total})
	return ToInteractionResponse(r, req.Model), nil
}
func (c *Client) headers() map[string]string { h := map[string]string{"x-goog-api-key": c.APIKey}; for k, v := range c.Headers { if v == "" || strings.EqualFold(k, "x-goog-api-key") { continue }; h[k] = v }; return h }
func cloneHeaders(in map[string]string) map[string]string { if len(in) == 0 { return nil }; out := make(map[string]string, len(in)); for k, v := range in { out[k] = v }; return out }

// ToInteractionResponse converts a native Interactions response into
// sdk.Response through the central OpenAI Responses shape.
func ToInteractionResponse(r interactionResponse, model string) sdk.Response {
	var texts []string
	var calls []sdk.ToolCall
	var reasoning *sdk.ReasoningState
	for _, step := range r.Steps {
		switch step.Type {
		case "model_output", "thought":
			for _, block := range step.Content {
				if block.Type != "text" || block.Text == "" {
					continue
				}
				if step.Type == "thought" {
					if reasoning == nil {
						reasoning = &sdk.ReasoningState{}
					}
					reasoning.Text += block.Text
				} else {
					texts = append(texts, block.Text)
				}
			}
		case "function_call":
			calls = append(calls, sdk.ToolCall{ID: step.ID, Name: step.Name, Arguments: normalizeArguments(step.Arguments)})
		}
	}
	// The interaction response carries no usage counters; accounting records
	// zeros for Gemini until the API exposes them on this endpoint.
	out := openai.ResponsesResponseFromParts(model, "", texts, calls, reasoning, sdk.Usage{})
	out.Provider = "gemini"
	out.Cache = sdk.CacheInfo{Layer: "provider"}
	return out
}

// normalizeArguments keeps the interaction function_call arguments as a
// canonical JSON string for the tool loop round-trip.
func normalizeArguments(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	normalized, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(normalized)
}
func (c *Client) http() *http.Client { if c.HTTP != nil { return c.HTTP }; return http.DefaultClient }
