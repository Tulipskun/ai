package internal

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDoJSONGetSendsNoBody(t *testing.T) {
	var method, contentType string
	var bodyLen int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		contentType = r.Header.Get("Content-Type")
		data, _ := io.ReadAll(r.Body)
		bodyLen = int64(len(data))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	var out struct{ OK bool }
	if err := DoJSON(context.Background(), server.Client(), http.MethodGet, server.URL, nil, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatal("response was not decoded")
	}
	if method != http.MethodGet {
		t.Fatalf("method = %q", method)
	}
	if contentType != "" {
		t.Fatalf("GET must not set Content-Type, got %q", contentType)
	}
	if bodyLen != 0 {
		t.Fatalf("GET must not send a body, got %d bytes", bodyLen)
	}
}

func TestDoJSONPostKeepsBody(t *testing.T) {
	var contentType string
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	var out struct{}
	if err := DoJSON(context.Background(), server.Client(), http.MethodPost, server.URL, nil, map[string]string{"a": "b"}, &out); err != nil {
		t.Fatal(err)
	}
	if contentType != "application/json" {
		t.Fatalf("POST Content-Type = %q", contentType)
	}
	if string(body) != `{"a":"b"}` {
		t.Fatalf("POST body = %q", body)
	}
}
