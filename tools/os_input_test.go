package tools

import (
	"bytes"
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

func TestOSInputExtendedDryRunTools(t *testing.T) {
	t.Setenv("AI_OS_INPUT_DRY_RUN", "1")
	r, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, args any) sdk.ToolResult {
		raw, _ := json.Marshal(args)
		return r.Execute(context.Background(), sdk.ToolCall{ID: "x", Name: name, Arguments: string(raw)})
	}

	drag := call("os_mouse_drag", map[string]any{"x1": 0, "y1": 0, "x2": 100, "y2": 50, "button": 1, "steps": 5})
	if drag.IsError {
		t.Fatalf("os_mouse_drag dry run failed: %s", drag.Content)
	}
	dragPayload := osInputDryRunPayload(t, drag.Content)
	if dragPayload["action"] != "mouse_drag" {
		t.Fatalf("mouse_drag action=%v", dragPayload["action"])
	}
	var dragArgv []any
	if err := json.Unmarshal([]byte(drag.Content), &struct {
		Argv *[]any `json:"argv"`
	}{Argv: &dragArgv}); err != nil {
		t.Fatal(err)
	}
	joined := drag.Content
	for _, want := range []string{"mousedown", "mouseup", "mousemove"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("drag argv missing %q: %s", want, joined)
		}
	}

	scroll := call("os_mouse_scroll", map[string]any{"direction": "up", "amount": 3})
	if scroll.IsError {
		t.Fatalf("os_mouse_scroll dry run failed: %s", scroll.Content)
	}
	if payload := osInputDryRunPayload(t, scroll.Content); payload["action"] != "mouse_scroll" {
		t.Fatalf("mouse_scroll action=%v", payload["action"])
	}
	if !strings.Contains(scroll.Content, `"button":4`) {
		t.Fatalf("scroll up must map to button 4: %s", scroll.Content)
	}

	shot := call("os_screenshot", map[string]any{"name": "desk.png"})
	if shot.IsError {
		t.Fatalf("os_screenshot dry run failed: %s", shot.Content)
	}
	if payload := osInputDryRunPayload(t, shot.Content); payload["action"] != "screenshot" {
		t.Fatalf("screenshot action=%v", payload["action"])
	}
	if !strings.Contains(shot.Content, "import") {
		t.Fatalf("screenshot argv must plan import: %s", shot.Content)
	}
	if strings.Contains(shot.Content, "iVBOR") {
		t.Fatalf("screenshot dry run must not carry bytes: %s", shot.Content)
	}

	list := call("os_window_list", map[string]any{"pattern": "term", "limit": 5})
	if list.IsError {
		t.Fatalf("os_window_list dry run failed: %s", list.Content)
	}
	if payload := osInputDryRunPayload(t, list.Content); payload["action"] != "window_list" {
		t.Fatalf("window_list action=%v", payload["action"])
	}

	focus := call("os_window_focus", map[string]any{"window_id": "12345"})
	if focus.IsError {
		t.Fatalf("os_window_focus dry run failed: %s", focus.Content)
	}
	if payload := osInputDryRunPayload(t, focus.Content); payload["action"] != "window_focus" {
		t.Fatalf("window_focus action=%v", payload["action"])
	}

	geom := call("os_window_geometry", map[string]any{})
	if geom.IsError {
		t.Fatalf("os_window_geometry dry run failed: %s", geom.Content)
	}
	if payload := osInputDryRunPayload(t, geom.Content); payload["action"] != "window_geometry" {
		t.Fatalf("window_geometry action=%v", payload["action"])
	}
}

func TestOSInputExtendedValidation(t *testing.T) {
	t.Setenv("AI_OS_INPUT_DRY_RUN", "1")
	r, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	call := func(name, args string) sdk.ToolResult {
		return r.Execute(context.Background(), sdk.ToolCall{ID: "x", Name: name, Arguments: args})
	}
	if res := call("os_mouse_drag", `{"x1":1,"y1":2}`); !res.IsError {
		t.Fatal("expected missing drag coords error")
	}
	if res := call("os_mouse_drag", `{"x1":0,"y1":0,"x2":1,"y2":1,"steps":99}`); !res.IsError {
		t.Fatal("expected bad steps error")
	}
	if res := call("os_mouse_drag", `{"x1":0,"y1":0,"x2":1,"y2":1,"button":10}`); !res.IsError {
		t.Fatal("expected bad drag button error")
	}
	if res := call("os_mouse_scroll", `{"direction":"sideways"}`); !res.IsError {
		t.Fatal("expected bad scroll direction error")
	}
	if res := call("os_mouse_scroll", `{"direction":"up","amount":99}`); !res.IsError {
		t.Fatal("expected bad scroll amount error")
	}
	if res := call("os_screenshot", `{"display":"bad;display"}`); !res.IsError {
		t.Fatal("expected bad display error")
	}
	if res := call("os_screenshot", `{"scale":2}`); !res.IsError {
		t.Fatal("expected bad scale error")
	}
	if res := call("os_window_list", `{"limit":999}`); !res.IsError {
		t.Fatal("expected bad list limit error")
	}
	if res := call("os_window_focus", `{}`); !res.IsError {
		t.Fatal("expected missing focus target error")
	}
	if res := call("os_window_focus", `{"window_id":"not a window!!"}`); !res.IsError {
		t.Fatal("expected bad window id error")
	}
	if res := call("os_window_geometry", `{"window_id":"!!"}`); !res.IsError {
		t.Fatal("expected bad geometry window id error")
	}
	// Clamping still applies in dry-run for drag endpoints.
	clamped := call("os_mouse_drag", `{"x1":-5,"y1":-5,"x2":99999,"y2":99999}`)
	if clamped.IsError {
		t.Fatalf("clamped drag failed: %s", clamped.Content)
	}
	if !strings.Contains(clamped.Content, `"x1":0`) || !strings.Contains(clamped.Content, `"x2":16384`) {
		t.Fatalf("drag endpoints not clamped: %s", clamped.Content)
	}
}

func TestOSMouseDragLiveChain(t *testing.T) {
	oldDry := osInputDryRun
	osInputDryRun = func() bool { return false }
	defer func() { osInputDryRun = oldDry }()
	oldRun := osInputRun
	var got []string
	osInputRun = func(ctx context.Context, name string, args []string) (string, error) {
		got = append([]string{name}, args...)
		return "", nil
	}
	defer func() { osInputRun = oldRun }()
	oldLook := osInputLookPath
	osInputLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	defer func() { osInputLookPath = oldLook }()
	t.Setenv("DISPLAY", ":0")
	r, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	res := r.Execute(context.Background(), sdk.ToolCall{ID: "x", Name: "os_mouse_drag", Arguments: `{"x1":0,"y1":0,"x2":10,"y2":0,"steps":2}`})
	if res.IsError {
		t.Fatalf("drag chain failed: %s", res.Content)
	}
	flat := strings.Join(got, " ")
	for _, want := range []string{"xdotool", "mousedown 1", "mouseup 1", "mousemove 10 0"} {
		if !strings.Contains(flat, want) {
			t.Fatalf("drag chain missing %q in %q", want, flat)
		}
	}
}

func TestOSWindowListParsesIDs(t *testing.T) {
	oldDry := osInputDryRun
	osInputDryRun = func() bool { return false }
	defer func() { osInputDryRun = oldDry }()
	oldRun := osInputRun
	calls := 0
	osInputRun = func(ctx context.Context, name string, args []string) (string, error) {
		calls++
		if args[0] == "search" {
			return "111\n222\n", nil
		}
		return "title", nil
	}
	defer func() { osInputRun = oldRun }()
	oldLook := osInputLookPath
	osInputLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	defer func() { osInputLookPath = oldLook }()
	t.Setenv("DISPLAY", ":0")
	r, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	res := r.Execute(context.Background(), sdk.ToolCall{ID: "x", Name: "os_window_list", Arguments: `{"limit":1}`})
	if res.IsError {
		t.Fatalf("window list failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, `"count":1`) || !strings.Contains(res.Content, `"id":"111"`) {
		t.Fatalf("window list not parsed/limited: %s", res.Content)
	}
	if calls != 2 {
		t.Fatalf("expected search+titles limited to 1, calls=%d", calls)
	}
}

func TestOSScreenshotStoresReferenceOnly(t *testing.T) {
	oldDry := osInputDryRun
	osInputDryRun = func() bool { return false }
	defer func() { osInputDryRun = oldDry }()
	oldLook := osInputLookPath
	osInputLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	defer func() { osInputLookPath = oldLook }()
	oldCap := osInputRunCapture
	png := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte{1}, 64)...)
	osInputRunCapture = func(ctx context.Context, name string, args []string) ([]byte, string, error) {
		if name != "import" {
			t.Fatalf("unexpected capture binary %q", name)
		}
		return png, "", nil
	}
	defer func() { osInputRunCapture = oldCap }()
	fx := newAttachmentFixture(t)
	fx.registry.SetAttachmentStore(fx.store)
	ctx := attachmentContext(attachmentSessionA)
	content, isErr := executeAttachment(t, fx.registry, ctx, "os_screenshot", map[string]any{"name": "desk.png"})
	if isErr {
		t.Fatalf("os_screenshot failed: %s", content)
	}
	if strings.Contains(content, "iVBOR") {
		t.Fatalf("screenshot result must not carry bytes: %s", content)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		t.Fatal(err)
	}
	refID, _ := payload["ref_id"].(string)
	if refID == "" {
		t.Fatalf("screenshot missing ref_id: %s", content)
	}
	listed, isErr := executeAttachment(t, fx.registry, ctx, "list_attachments", map[string]any{})
	if isErr || !strings.Contains(listed, refID) {
		t.Fatalf("screenshot not in attachment store: %s err=%v", listed, isErr)
	}
	described, isErr := executeAttachment(t, fx.registry, ctx, "describe_attachment", map[string]any{"ref_id": refID})
	if isErr {
		t.Fatalf("screenshot not describable: %s", described)
	}
	if !strings.Contains(described, "image/png") {
		t.Fatalf("screenshot not stored as PNG: %s", described)
	}
}

func TestOSEffectiveDisplayFallback(t *testing.T) {
	oldProbe := osFallbackDisplayProbe
	defer func() { osFallbackDisplayProbe = oldProbe }()

	t.Setenv("DISPLAY", ":0")
	osFallbackDisplayProbe = func() string { return ":1" }
	if got := effectiveOSDisplay(); got != ":0" {
		t.Fatalf("explicit DISPLAY must win, got %q", got)
	}

	t.Setenv("DISPLAY", "")
	osFallbackDisplayProbe = func() string { return ":1" }
	if got := effectiveOSDisplay(); got != ":1" {
		t.Fatalf("empty DISPLAY must fall back to :1, got %q", got)
	}

	t.Setenv("DISPLAY", "")
	osFallbackDisplayProbe = func() string { return "" }
	if got := effectiveOSDisplay(); got != "" {
		t.Fatalf("no DISPLAY and no socket must yield empty, got %q", got)
	}
}

func TestOSMouseMoveFallsBackToDisplayOne(t *testing.T) {
	oldDry := osInputDryRun
	osInputDryRun = func() bool { return false }
	defer func() { osInputDryRun = oldDry }()
	oldProbe := osFallbackDisplayProbe
	osFallbackDisplayProbe = func() string { return ":1" }
	defer func() { osFallbackDisplayProbe = oldProbe }()
	oldRun := osInputRun
	osInputRun = func(ctx context.Context, name string, args []string) (string, error) {
		if name != "xdotool" {
			t.Fatalf("unexpected binary %q", name)
		}
		return "", nil
	}
	defer func() { osInputRun = oldRun }()
	oldLook := osInputLookPath
	osInputLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	defer func() { osInputLookPath = oldLook }()
	t.Setenv("DISPLAY", "")
	r, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	res := r.Execute(context.Background(), sdk.ToolCall{ID: "x", Name: "os_mouse_move", Arguments: `{"x":10,"y":20}`})
	if res.IsError {
		t.Fatalf("mouse move with :1 fallback failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, `"ok":true`) {
		t.Fatalf("mouse move missing ok payload: %s", res.Content)
	}
}

func TestOSMouseMoveNoDisplayNoSocketStillErrors(t *testing.T) {
	oldDry := osInputDryRun
	osInputDryRun = func() bool { return false }
	defer func() { osInputDryRun = oldDry }()
	oldProbe := osFallbackDisplayProbe
	osFallbackDisplayProbe = func() string { return "" }
	defer func() { osFallbackDisplayProbe = oldProbe }()
	oldLook := osInputLookPath
	osInputLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	defer func() { osInputLookPath = oldLook }()
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	r, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	res := r.Execute(context.Background(), sdk.ToolCall{ID: "x", Name: "os_mouse_move", Arguments: `{"x":10,"y":20}`})
	if !res.IsError || !strings.Contains(res.Content, "DISPLAY is not set") {
		t.Fatalf("expected DISPLAY error, got: %s", res.Content)
	}
}
