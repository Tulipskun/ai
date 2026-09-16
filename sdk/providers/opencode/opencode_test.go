package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

var sessionFormat = regexp.MustCompile(`^ses_f[0-9a-f]{8}ffe[A-Za-z0-9]{14}$`)

func TestSessionIDFormat(t *testing.T) {
	id := SessionIDFor("test-format")
	if !sessionFormat.MatchString(id) {
		t.Fatalf("session id %q does not match opencode shape", id)
	}
	if len(id) != 30 {
		t.Fatalf("session id length = %d, want 30", len(id))
	}
}

func TestSessionIDStablePerKey(t *testing.T) {
	first := SessionIDFor("stable-key")
	for i := 0; i < 5; i++ {
		if got := SessionIDFor("stable-key"); got != first {
			t.Fatalf("session id changed within process: %q vs %q", first, got)
		}
	}
	if other := SessionIDFor("other-key"); other == first {
		t.Fatal("distinct harness sessions share one provider session id")
	}
}

func TestSessionIDEmptyKeyMintsFresh(t *testing.T) {
	if a, b := SessionIDFor(""), SessionIDFor(""); a == b {
		t.Fatal("empty key must not be cached")
	}
}

func checkFingerprint(t *testing.T, h http.Header, sessionKey string) {
	t.Helper()
	if got := h.Get("User-Agent"); got != DefaultUserAgent {
		t.Fatalf("User-Agent=%q, want %q", got, DefaultUserAgent)
	}
	if got := h.Get("HTTP-Referer"); got != Referer {
		t.Fatalf("HTTP-Referer=%q", got)
	}
	if got := h.Get("X-Title"); got != Title {
		t.Fatalf("X-Title=%q", got)
	}
	sid := h.Get("x-opencode-session")
	if !sessionFormat.MatchString(sid) {
		t.Fatalf("x-opencode-session=%q, want opencode shape", sid)
	}
	if sessionKey != "" && sid != SessionIDFor(sessionKey) {
		t.Fatal("session header does not match the harness session mapping")
	}
}

func TestGeneratePrefersResponses(t *testing.T) {
	var gotHeaders http.Header
	var gotBody map[string]any
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		gotHeaders = r.Header
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"id":"resp-1","model":"m","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hi"}]}]}`))
	}))
	defer srv.Close()
	c := New("k")
	c.BaseURL = srv.URL
	resp, err := c.Generate(context.Background(), sdk.Request{Model: "m", SessionID: "harness-1", Messages: []sdk.Turn{{Role: sdk.RoleUser, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "hi"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "hi" {
		t.Fatalf("content=%+v", resp.Content)
	}
	if len(hits) != 1 || hits[0] != "/responses" {
		t.Fatalf("must use /responses first without chat fallback: %v", hits)
	}
	checkFingerprint(t, gotHeaders, "harness-1")
	if _, ok := gotBody["user"]; ok {
		t.Fatal("request body must not carry a user field (opencode sends the session via header only)")
	}
	if gotBody["model"] != "m" {
		t.Fatalf("model=%v", gotBody["model"])
	}
	if _, ok := gotBody["input"]; !ok {
		t.Fatal("responses body must carry input items")
	}
}

func TestCustomHeadersOverrideExceptSession(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header
		_, _ = w.Write([]byte(`{"id":"resp-1","status":"completed","output":[]}`))
	}))
	defer srv.Close()
	c := New("k").WithHeaders(map[string]string{"User-Agent": "custom/1", "x-opencode-session": "ses_forged"}).(*Client)
	c.BaseURL = srv.URL
	if _, err := c.Generate(context.Background(), sdk.Request{Model: "m", SessionID: "harness-2"}); err != nil {
		t.Fatal(err)
	}
	if got := gotHeaders.Get("User-Agent"); got != "custom/1" {
		t.Fatalf("custom User-Agent lost: %q", got)
	}
	if got := gotHeaders.Get("x-opencode-session"); got != SessionIDFor("harness-2") {
		t.Fatalf("session header was overridden: %q", got)
	}
}

func TestGenerateParsesToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model":"m","status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"bash","arguments":"{\"command\":\"ls\"}"}]}`))
	}))
	defer srv.Close()
	c := New("k")
	c.BaseURL = srv.URL
	resp, err := c.Generate(context.Background(), sdk.Request{Model: "m", SessionID: "tools-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "bash" || resp.ToolCalls[0].ID != "call_1" {
		t.Fatalf("tool calls=%+v", resp.ToolCalls)
	}
}

func TestGenerateFallsBackToChatOnResponses500(t *testing.T) {
	var hits []string
	var chatHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		if r.URL.Path == "/responses" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"error","message":"Internal server error"}}`))
			return
		}
		chatHeaders = r.Header
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"message":{"content":"via-chat"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	c := New("k")
	c.BaseURL = srv.URL
	resp, err := c.Generate(context.Background(), sdk.Request{Model: "m", SessionID: "fallback-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "via-chat" {
		t.Fatalf("content=%+v", resp.Content)
	}
	if len(hits) != 2 || hits[0] != "/responses" || hits[1] != "/chat/completions" {
		t.Fatalf("must fall back to chat after responses 500: %v", hits)
	}
	checkFingerprint(t, chatHeaders, "fallback-1")
}

func TestGenerateNoFallbackOnRateLimit(t *testing.T) {
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()
	c := New("k")
	c.BaseURL = srv.URL
	if _, err := c.Generate(context.Background(), sdk.Request{Model: "m", SessionID: "limited-1"}); err == nil {
		t.Fatal("rate limit must surface, not fall back")
	}
	if len(hits) != 1 || hits[0] != "/responses" {
		t.Fatalf("must not touch chat on 429: %v", hits)
	}
}

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Header.Get("x-opencode-session") == "" {
			t.Fatal("models request misses session header")
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"a"},{"id":"b"}]}`))
	}))
	defer srv.Close()
	c := New("k")
	c.BaseURL = srv.URL
	models, err := c.ListModels(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "a" || models[1].ID != "b" {
		t.Fatalf("models=%+v", models)
	}
}

func collectStream(t *testing.T, ch <-chan sdk.Event) (string, int, bool) {
	t.Helper()
	var text strings.Builder
	var calls int
	var done bool
	for ev := range ch {
		switch ev.Type {
		case sdk.EventText:
			text.WriteString(ev.Text)
		case sdk.EventToolCall:
			calls++
			if ev.ToolCall.Name != "bash" || ev.ToolCall.ID != "c1" {
				t.Fatalf("tool call=%+v", ev.ToolCall)
			}
		case sdk.EventDone:
			done = true
		case sdk.EventError:
			t.Fatal(ev.Err)
		}
	}
	return text.String(), calls, done
}

func TestStreamResponsesDirect(t *testing.T) {
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"he\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"llo\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.function_call_arguments.done\",\"item\":{\"call_id\":\"c1\",\"name\":\"bash\",\"arguments\":\"{}\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	defer srv.Close()
	c := New("k")
	c.BaseURL = srv.URL
	ch, err := c.Stream(context.Background(), sdk.Request{Model: "m", SessionID: "stream-1"})
	if err != nil {
		t.Fatal(err)
	}
	text, calls, done := collectStream(t, ch)
	if text != "hello" || calls != 1 || !done {
		t.Fatalf("text=%q calls=%d done=%v", text, calls, done)
	}
	if len(hits) != 1 || hits[0] != "/responses" {
		t.Fatalf("must stream /responses without chat fallback: %v", hits)
	}
}

func TestStreamFallsBackToChat(t *testing.T) {
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		if r.URL.Path == "/responses" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"type":"error"}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"llo\",\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"bash\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	c := New("k")
	c.BaseURL = srv.URL
	ch, err := c.Stream(context.Background(), sdk.Request{Model: "m", SessionID: "stream-2"})
	if err != nil {
		t.Fatal(err)
	}
	text, calls, done := collectStream(t, ch)
	if text != "hello" || calls != 1 || !done {
		t.Fatalf("text=%q calls=%d done=%v", text, calls, done)
	}
	if len(hits) != 2 || hits[0] != "/responses" || hits[1] != "/chat/completions" {
		t.Fatalf("must fall back to chat stream: %v", hits)
	}
}

func TestResponsesBodyUsesDeveloperInput(t *testing.T) {
	req := newResponsesBodyRequest()
	body := buildResponsesRequest(req)
	if _, ok := body["instructions"]; ok {
		t.Fatal("responses body must not use the instructions field")
	}
	items, ok := body["input"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("input items = %v", body["input"])
	}
	first, ok := items[0].(map[string]any)
	if !ok || first["role"] != "developer" || first["content"] != "sys" {
		t.Fatalf("first input must be the developer system prompt: %v", items[0])
	}
}

func newResponsesBodyRequest() sdk.Request {
	return sdk.Request{Model: "m", SystemPrompt: "sys", Messages: []sdk.Turn{{Role: sdk.RoleUser, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "hi"}}}}}
}
