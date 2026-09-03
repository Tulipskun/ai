package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBrowserClientCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": req["id"], "ok": true,
			"result": map[string]any{"value": "ok"},
		})
	}))
	defer srv.Close()

	client := NewBrowserClientForTest(srv.URL, "test-token", BrowserClientConfig{RPCTimeout: time.Second})
	var result struct{ Value string `json:"value"` }
	if err := client.Call(context.Background(), "browser.open", map[string]string{"session_id": "a"}, &result); err != nil {
		t.Fatal(err)
	}
	if result.Value != "ok" {
		t.Fatalf("result = %#v", result)
	}
}

func TestBrowserClientDecodesWorkerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "1", "ok": false,
			"error": map[string]any{"code": "stale_reference", "message": "stale"},
		})
	}))
	defer srv.Close()

	client := NewBrowserClientForTest(srv.URL, "test-token", BrowserClientConfig{RPCTimeout: time.Second})
	var result any
	err := client.Call(context.Background(), "browser.click", nil, &result)
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("error = %v", err)
	}
}

func TestBrowserClientHonorsContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte(`{"id":"1","ok":true,"result":{}}`))
	}))
	defer srv.Close()

	client := NewBrowserClientForTest(srv.URL, "test-token", BrowserClientConfig{RPCTimeout: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := client.Call(ctx, "browser.snapshot", nil, nil); err == nil {
		t.Fatal("expected context timeout")
	}
}

func TestBrowserClientReportsWorkerStderrOnStartupFailure(t *testing.T) {
	dir := t.TempDir()
	worker := dir + "/worker.sh"
	if err := os.WriteFile(worker, []byte("#!/bin/sh\necho 'worker failed: chromium unavailable' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	client := NewBrowserClient(BrowserClientConfig{
		NodeCommand:    "/bin/sh",
		WorkerPath:     worker,
		WorkerDir:      dir,
		StartupTimeout: time.Second,
	})
	err := client.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "worker failed: chromium unavailable") {
		t.Fatalf("error = %v", err)
	}
}
