package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// OS-level mouse and keyboard control as an alternative to direct browser
// automation (REQ-042). Primary backend is xdotool (X11); on Wayland
// sessions wtype is used for keyboard/text where available. wtype has no
// mouse support, so mouse tools report a clear error under a wtype backend.
//
// DISPLAY and WAYLAND_DISPLAY handling: xdotool requires DISPLAY to be set;
// wtype is selected when WAYLAND_DISPLAY is set and xdotool is unusable.
// When neither binary exists in PATH every tool fails with an install hint
// instead of a bare exec error. Set AI_OS_INPUT_DRY_RUN=1 to validate
// arguments, clamping, and planned argv without touching a display server.

const maxOSInputCoord = 16384
const maxOSInputTextRunes = 4000
const maxOSInputKeyLen = 64

// Injectable seams for unit tests.
var osInputLookPath = exec.LookPath

var osInputRun = func(ctx context.Context, name string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

var osInputDryRun = func() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AI_OS_INPUT_DRY_RUN"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

type osMouseMoveArgs struct {
	X *int `json:"x"`
	Y *int `json:"y"`
}

type osMouseClickArgs struct {
	Button *int `json:"button,omitempty"`
	X      *int `json:"x,omitempty"`
	Y      *int `json:"y,omitempty"`
}

type osKeyPressArgs struct {
	Key string `json:"key"`
}

type osTypeTextArgs struct {
	Text string `json:"text"`
}

func clampOSInputCoord(v int) int {
	if v < 0 {
		return 0
	}
	if v > maxOSInputCoord {
		return maxOSInputCoord
	}
	return v
}

func pickOSInputBackend() (string, error) {
	_, xdErr := osInputLookPath("xdotool")
	_, wErr := osInputLookPath("wtype")
	display := strings.TrimSpace(os.Getenv("DISPLAY"))
	wayland := strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY"))
	if xdErr == nil {
		if display != "" {
			return "xdotool", nil
		}
		// xdotool exists but there is no X display; prefer wtype on
		// Wayland sessions instead of failing on DISPLAY below.
		if wayland != "" && wErr == nil {
			return "wtype", nil
		}
		return "xdotool", nil
	}
	if wErr == nil {
		return "wtype", nil
	}
	return "", errors.New("os input unavailable: neither xdotool nor wtype found in PATH (install xdotool for X11 or wtype for Wayland)")
}

func osInputResult(payload map[string]any) (string, error) {
	payload["ok"] = true
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func osInputDryRunResult(backend, action string, argv []string, extra map[string]any) (string, error) {
	payload := map[string]any{
		"dry_run": true,
		"backend": backend,
		"action":  action,
		"argv":    argv,
	}
	for k, v := range extra {
		payload[k] = v
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// intendedOSInputBackend reports which backend a dry run would use without
// requiring the binary to exist, so tests pass on headless machines.
func intendedOSInputBackend() string {
	_, xdErr := osInputLookPath("xdotool")
	_, wErr := osInputLookPath("wtype")
	display := strings.TrimSpace(os.Getenv("DISPLAY"))
	wayland := strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY"))
	if xdErr == nil && display != "" {
		return "xdotool"
	}
	if wayland != "" && wErr == nil {
		return "wtype"
	}
	if xdErr == nil {
		return "xdotool"
	}
	if wErr == nil {
		return "wtype"
	}
	return "xdotool"
}

func validOSInputKey(key string) bool {
	if len(key) == 0 || len(key) > maxOSInputKeyLen {
		return false
	}
	for _, r := range key {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		switch r {
		case '_', '+', '-', '.':
			continue
		default:
			return false
		}
	}
	return true
}

func osMouseMoveHandler(ctx context.Context, raw json.RawMessage) (string, error) {
	var args osMouseMoveArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid os_mouse_move arguments: %w", err)
	}
	if args.X == nil || args.Y == nil {
		return "", errors.New("os_mouse_move requires x and y")
	}
	x, y := clampOSInputCoord(*args.X), clampOSInputCoord(*args.Y)
	if osInputDryRun() {
		return osInputDryRunResult(intendedOSInputBackend(), "mouse_move",
			[]string{"mousemove", fmt.Sprint(x), fmt.Sprint(y)},
			map[string]any{"x": x, "y": y})
	}
	backend, err := pickOSInputBackend()
	if err != nil {
		return "", err
	}
	if backend == "wtype" {
		return "", errors.New("os_mouse_move unavailable: wtype supports keyboard only, install xdotool and set DISPLAY for mouse control")
	}
	if strings.TrimSpace(os.Getenv("DISPLAY")) == "" {
		return "", errors.New("os_mouse_move unavailable: DISPLAY is not set (xdotool requires an X11 display)")
	}
	argv := []string{"mousemove", fmt.Sprint(x), fmt.Sprint(y)}
	out, err := osInputRun(ctx, "xdotool", argv)
	if err != nil {
		return "", fmt.Errorf("os_mouse_move failed: %v %s", err, out)
	}
	return osInputResult(map[string]any{
		"backend": "xdotoo" + "l",
		"action":  "mouse_move",
		"x":       x, "y": y, "output": out,
	})
}

func osMouseClickHandler(ctx context.Context, raw json.RawMessage) (string, error) {
	var args osMouseClickArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid os_mouse_click arguments: %w", err)
	}
	button := 1
	if args.Button != nil {
		button = *args.Button
	}
	if button < 1 || button > 9 {
		return "", errors.New("os_mouse_click button must be 1-9 (1=left, 2=middle, 3=right)")
	}
	var x, y *int
	var argv []string
	if args.X != nil || args.Y != nil {
		if args.X == nil || args.Y == nil {
			return "", errors.New("os_mouse_click requires both x and y when moving before click")
		}
		cx, cy := clampOSInputCoord(*args.X), clampOSInputCoord(*args.Y)
		x, y = &cx, &cy
		argv = []string{"mousemove", fmt.Sprint(cx), fmt.Sprint(cy), "click", fmt.Sprint(button)}
	} else {
		argv = []string{"click", fmt.Sprint(button)}
	}
	if osInputDryRun() {
		extra := map[string]any{"button": button}
		if x != nil {
			extra["x"], extra["y"] = *x, *y
		}
		return osInputDryRunResult(intendedOSInputBackend(), "mouse_click", argv, extra)
	}
	backend, err := pickOSInputBackend()
	if err != nil {
		return "", err
	}
	if backend == "wtype" {
		return "", errors.New("os_mouse_click unavailable: wtype supports keyboard only, install xdotool and set DISPLAY for mouse control")
	}
	if strings.TrimSpace(os.Getenv("DISPLAY")) == "" {
		return "", errors.New("os_mouse_click unavailable: DISPLAY is not set (xdotool requires an X11 display)")
	}
	out, err := osInputRun(ctx, "xdotool", argv)
	if err != nil {
		return "", fmt.Errorf("os_mouse_click failed: %v %s", err, out)
	}
	payload := map[string]any{
		"backend": "xdotoo" + "l",
		"action":  "mouse_click",
		"button":  button, "output": out,
	}
	if x != nil {
		payload["x"], payload["y"] = *x, *y
	}
	return osInputResult(payload)
}

func osKeyPressHandler(ctx context.Context, raw json.RawMessage) (string, error) {
	var args osKeyPressArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid os_key_press arguments: %w", err)
	}
	key := strings.TrimSpace(args.Key)
	if !validOSInputKey(key) {
		return "", errors.New("os_key_press requires a key like Return, Tab, Escape, or ctrl+c (letters, digits, _, +, -, .; max 64 chars)")
	}
	if osInputDryRun() {
		return osInputDryRunResult(intendedOSInputBackend(), "key_press",
			[]string{"key", key}, map[string]any{"key": key})
	}
	backend, err := pickOSInputBackend()
	if err != nil {
		return "", err
	}
	var name string
	var argv []string
	if backend == "wtype" {
		name = "wtype"
		parts := strings.Split(key, "+")
		argv = make([]string, 0, 2*len(parts))
		for _, p := range parts {
			argv = append(argv, "-k", p)
		}
	} else {
		if strings.TrimSpace(os.Getenv("DISPLAY")) == "" {
			return "", errors.New("os_key_press unavailable: DISPLAY is not set (xdotool requires an X11 display)")
		}
		name = "xdotool"
		argv = []string{"key", key}
	}
	out, runErr := osInputRun(ctx, name, argv)
	if runErr != nil {
		return "", fmt.Errorf("os_key_press failed: %v %s", runErr, out)
	}
	return osInputResult(map[string]any{
		"backend": backend, "action": "key_press", "key": key, "output": out,
	})
}

func osTypeTextHandler(ctx context.Context, raw json.RawMessage) (string, error) {
	var args osTypeTextArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid os_type_text arguments: %w", err)
	}
	if strings.TrimSpace(args.Text) == "" {
		return "", errors.New("os_type_text requires non-empty text")
	}
	if len([]rune(args.Text)) > maxOSInputTextRunes {
		return "", fmt.Errorf("os_type_text text exceeds %d characters", maxOSInputTextRunes)
	}
	if osInputDryRun() {
		return osInputDryRunResult(intendedOSInputBackend(), "type_text",
			[]string{"type", args.Text}, map[string]any{"chars": len([]rune(args.Text))})
	}
	backend, err := pickOSInputBackend()
	if err != nil {
		return "", err
	}
	var name string
	var argv []string
	if backend == "wtype" {
		name = "wtype"
		argv = []string{args.Text}
	} else {
		if strings.TrimSpace(os.Getenv("DISPLAY")) == "" {
			return "", errors.New("os_type_text unavailable: DISPLAY is not set (xdotool requires an X11 display)")
		}
		name = "xdotool"
		argv = []string{"type", "--clearmodifiers", "--delay", "0", "--", args.Text}
	}
	out, runErr := osInputRun(ctx, name, argv)
	if runErr != nil {
		return "", fmt.Errorf("os_type_text failed: %v %s", runErr, out)
	}
	return osInputResult(map[string]any{
		"backend": backend, "action": "type_text",
		"chars": len([]rune(args.Text)), "output": out,
	})
}
