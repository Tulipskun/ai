package tools

import (
	"context"
	"testing"
)

func TestBrowserCDPHTTPURL(t *testing.T) {
	cases := []struct{ endpoint, suffix, want string }{
		{"127.0.0.1:9222", "/json/version", "http://127.0.0.1:9222/json/version"},
		{"http://localhost:9222/", "/json/list", "http://localhost:9222/json/list"},
	}
	for _, tc := range cases {
		got, err := browserCDPHTTPURL(tc.endpoint, tc.suffix)
		if err != nil { t.Fatal(err) }
		if got != tc.want { t.Fatalf("got %q, want %q", got, tc.want) }
	}
}

func TestBrowserCDPHTTPURLRejectsUnsupportedScheme(t *testing.T) {
	if _, err := browserCDPHTTPURL("ws://127.0.0.1:9222", "/json/version"); err == nil { t.Fatal("expected unsupported scheme error") }
}

func TestBrowserListPagesRequiresConnection(t *testing.T) {
	client := NewBrowserClient(BrowserClientConfig{})
	if _, err := client.ListPages(context.Background()); err == nil { t.Fatal("expected unavailable browser error") }
}
