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

func TestGenerateSendsOpenCodeFingerprint(t *testing.T) {
	var gotHeaders http.Header
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"id":"gen-1","model":"m","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
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
	if got := gotHeaders.Get("User-Agent"); got != DefaultUserAgent {
		t.Fatalf("User-Agent=%q, want %q", got, DefaultUserAgent)
	}
	if got := gotHeaders.Get("HTTP-Referer"); got != Referer {
		t.Fatalf("HTTP-Referer=%q", got)
	}
	if got := gotHeaders.Get("X-Title"); got != Title {
		t.Fatalf("X-Title=%q", got)
	}
	sid := gotHeaders.Get("x-opencode-session")
	if !sessionFormat.MatchString(sid) {
		t.Fatalf("x-opencode-session=%q, want opencode shape", sid)
	}
	if sid != SessionIDFor("harness-1") {
		t.Fatal("session header does not match the harness session mapping")
	}
	if _, ok := gotBody["user"]; ok {
		t.Fatal("request body must not carry a user field (opencode sends the session via header only)")
	}
	if gotBody["model"] != "m" {
		t.Fatalf("model=%v", gotBody["model"])
	}
}

func TestCustomHeadersOverrideExceptSession(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
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
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]},"finish_reason":"tool_calls"}]}`))
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

func TestStreamChatDeltas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"llo\",\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"bash\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	c := New("k")
	c.BaseURL = srv.URL
	ch, err := c.Stream(context.Background(), sdk.Request{Model: "m", SessionID: "stream-1"})
	if err != nil {
		t.Fatal(err)
	}
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
	if text.String() != "hello" || calls != 1 || !done {
		t.Fatalf("text=%q calls=%d done=%v", text.String(), calls, done)
	}
}
