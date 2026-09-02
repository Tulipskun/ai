package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWebFetchHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><title>Example title</title><style>hidden</style></head><body><h1>Hello</h1><p>Readable <b>content</b>.</p><script>secret()</script></body></html>`)
	}))
	defer srv.Close()

	tool := newWebFetchTool(NewNetworkPolicy(true))
	content, err := tool(context.Background(), mustRawJSON(t, map[string]string{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, `"title":"Example title"`) || !strings.Contains(content, "Readable content.") {
		t.Fatalf("unexpected result: %s", content)
	}
	if strings.Contains(content, "secret()") || strings.Contains(content, "<h1>") {
		t.Fatalf("raw/untrusted markup leaked into result: %s", content)
	}
}

func TestWebFetchPlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "plain text response")
	}))
	defer srv.Close()

	tool := newWebFetchTool(NewNetworkPolicy(true))
	content, err := tool(context.Background(), mustRawJSON(t, map[string]string{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "plain text response") {
		t.Fatalf("unexpected result: %s", content)
	}
}

func TestWebFetchRedirectRevalidates(t *testing.T) {
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer blocked.Close()

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, blocked.URL, http.StatusFound)
	}))
	defer redirect.Close()

	tool := newWebFetchTool(NewNetworkPolicy(false))
	if _, err := tool(context.Background(), mustRawJSON(t, map[string]string{"url": redirect.URL})); err == nil {
		t.Fatal("expected redirect destination to be rejected")
	}
}

func TestWebFetchTruncates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, strings.Repeat("x", 256))
	}))
	defer srv.Close()

	tool := newWebFetchToolWithLimits(NewNetworkPolicy(true), 128, 32, 2*time.Second, 3)
	content, err := tool(context.Background(), mustRawJSON(t, map[string]string{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, `"truncated":true`) {
		t.Fatalf("expected truncation: %s", content)
	}
}

func TestWebFetchRejectsInvalidURL(t *testing.T) {
	tool := newWebFetchTool(NewNetworkPolicy(false))
	if _, err := tool(context.Background(), mustRawJSON(t, map[string]string{"url": "file:///tmp/a"})); err == nil {
		t.Fatal("expected invalid scheme error")
	}
}

func mustRawJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
