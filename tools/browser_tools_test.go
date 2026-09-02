package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrowserToolMapsArguments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Method != "browser.fill" {
			t.Fatalf("method=%s", request.Method)
		}
		if request.Params["session_id"] != "s" || request.Params["ref"] != "e1" || request.Params["text"] != "hello" {
			t.Fatalf("params=%#v", request.Params)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "1", "ok": true, "result": map[string]any{"ok": true}})
	}))
	defer srv.Close()

	client := NewBrowserClientForTest(srv.URL, "token", BrowserClientConfig{})
	tool := newBrowserTool(client, "browser.fill", decodeJSON[browserFillArgs])
	content, err := tool(context.Background(), mustRawJSON(t, browserFillArgs{SessionID: "s", Ref: "e1", Text: "hello"}))
	if err != nil {
		t.Fatal(err)
	}
	if content != `{"ok":true}` {
		t.Fatalf("content=%s", content)
	}
}
