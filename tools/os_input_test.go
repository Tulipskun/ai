package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func osInputDryRunPayload(t *testing.T, raw string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("dry-run payload not JSON: %v: %s", err, raw)
	}
	if payload["ok"] != nil {
		t.Fatalf("dry-run payload must not set ok: %s", raw)
	}
	if payload["dry_run"] != true {
		t.Fatalf("dry-run payload missing dry_run=true: %s", raw)
	}
	return payload
}

func TestOSInputDryRunTools(t *testing.T) {
	t.Setenv("AI_OS_INPUT_DRY_RUN", "1")
	r, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, args any) sdk.ToolResult {
		raw, _ := json.Marshal(args)
		return r.Execute(context.Background(), sdk.ToolCall{ID: "x", Name: name, Arguments: string(raw)})
	}

	move := call("os_mouse_move", map[string]any{"x": 100, "y": 200})
	if move.IsError {
		t.Fatalf("os_mouse_move dry run failed: %s", move.Content)
	}
	movePayload := osInputDryRunPayload(t, move.Content)
	if movePayload["action"] != "mouse_move" {
		t.Fatalf("mouse_move action=%v", movePayload["action"])
	}

	click := call("os_mouse_click", map[string]any{"button": 3, "x": 10, "y": 20})
	if click.IsError {
		t.Fatalf("os_mouse_click dry run failed: %s", click.Content)
	}
	clickPayload := osInputDryRunPayload(t, click.Content)
	if clickPayload["action"] != "mouse_click" {
		t.Fatalf("mouse_click action=%v", clickPayload["action"])
	}

	key := call("os_key_press", map[string]any{"key": "Return"})
	if key.IsError {
		t.Fatalf("os_key_press dry run failed: %s", key.Content)
	}
	keyPayload := osInputDryRunPayload(t, key.Content)
	if keyPayload["action"] != "key_press" || keyPayload["key"] != "Return" {
		t.Fatalf("key_press payload=%s", key.Content)
	}

	typed := call("os_type_text", map[string]any{"text": "hello"})
	if typed.IsError {
		t.Fatalf("os_type_text dry run failed: %s", typed.Content)
	}
	typedPayload := osInputDryRunPayload(t, typed.Content)
	if typedPayload["action"] != "type_text" {
		t.Fatalf("type_text action=%v", typedPayload["action"])
	}
	for _, res := range []sdk.ToolResult{move, click, key, typed} {
		var payload map[string]any
		if err := json.Unmarshal([]byte(res.Content), &payload); err != nil {
			t.Fatal(err)
		}
		argv, ok := payload["argv"].([]any)
		if !ok || len(argv) == 0 {
			t.Fatalf("dry-run payload missing planned argv: %s", res.Content)
		}
	}
}

func TestOSInputDryRunValidation(t *testing.T) {
	t.Setenv("AI_OS_INPUT_DRY_RUN", "1")
	r, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	call := func(name, args string) sdk.ToolResult {
		return r.Execute(context.Background(), sdk.ToolCall{ID: "x", Name: name, Arguments: args})
	}
	if res := call("os_mouse_move", `{"x":1}`); !res.IsError {
		t.Fatal("expected missing y error")
	}
	if res := call("os_mouse_click", `{"button":10}`); !res.IsError {
		t.Fatal("expected bad button error")
	}
	if res := call("os_key_press", `{"key":"bad key!"}`); !res.IsError {
		t.Fatal("expected bad key error")
	}
	if res := call("os_type_text", `{"text":"   "}`); !res.IsError {
		t.Fatal("expected empty text error")
	}
	long := call("os_type_text", `{"text":"`+strings.Repeat("a", maxOSInputTextRunes+1)+`"}`)
	if !long.IsError {
		t.Fatal("expected over-limit text error")
	}
	if _, ok := os.LookupEnv("AI_OS_INPUT_DRY_RUN_UNSET_CHECK"); ok {
		t.Fatal("unexpected env")
	}
	if os.Getenv("AI_OS_INPUT_DRY_RUN") == "" {
		t.Fatal("dry-run env must stay set during validation")
	}
}
