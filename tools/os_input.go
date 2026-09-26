package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Tulipskun/ai-engine/sdk"
)

// OS-level mouse and keyboard control as an alternative to direct browser
// automation (REQ-042). Primary backend is xdotool (X11); on Wayland
// sessions wtype is used for keyboard/text where available. wtype has no
// mouse support, so mouse tools report a clear error under a wtype backend.
//
// The full suite adds screen capture plus window management: os_screenshot
// stores a PNG in the session attachment store (reference only, never bytes
// on the text path), os_mouse_drag performs a smooth button-held drag,
// os_mouse_scroll sends wheel clicks, and os_window_list/focus/geometry
// manage X11 windows via xdotool search and getwindowgeometry.
//
// DISPLAY and WAYLAND_DISPLAY handling: xdotool uses effectiveOSDisplay(),
// which is $DISPLAY when set and otherwise falls back to :1 when the :1 X
// socket exists (the daemon often runs without DISPLAY while termux-x11
// still serves :1); wtype is selected when WAYLAND_DISPLAY is set and
// xdotool is unusable.
// When neither binary exists in PATH every tool fails with an install hint
// instead of a bare exec error. Set AI_OS_INPUT_DRY_RUN=1 to validate
// arguments, clamping, and planned argv without touching a display server.

const maxOSInputCoord = 16384
const maxOSInputTextRunes = 4000
const maxOSInputKeyLen = 64
const maxOSInputDragSteps = 50
const maxOSInputScrollAmount = 20
const maxOSInputPatternRunes = 256
const maxOSInputWindowIDLen = 20
const maxOSInputDisplayLen = 64
const maxOSInputScreenshotNameLen = 128
const maxOSWindowListDefault = 50
const maxOSWindowListMax = 200

var osPNGMagic = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// Injectable seams for unit tests.
var osInputLookPath = exec.LookPath

var osInputRun = func(ctx context.Context, name string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	withFallbackDisplayEnv(cmd)
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

// osFallbackDisplayProbe reports ":1" when the :1 X socket exists. The daemon
// is often started without DISPLAY in its environment (e.g. via supervisor)
// while termux-x11 still serves :1, so xdotool paths fall back to :1 instead
// of failing on empty DISPLAY. Injectable seam for unit tests.
var osFallbackDisplayProbe = func() string {
	if _, err := os.Stat("/tmp/.X11-unix/X1"); err == nil {
		return ":1"
	}
	return ""
}

// effectiveOSDisplay returns $DISPLAY when set, otherwise the :1 fallback
// when its socket exists, otherwise "".
func effectiveOSDisplay() string {
	if d := strings.TrimSpace(os.Getenv("DISPLAY")); d != "" {
		return d
	}
	return osFallbackDisplayProbe()
}

// withFallbackDisplayEnv injects DISPLAY=:1 into cmd.Env when the process
// environment has no DISPLAY but the :1 socket exists. An existing DISPLAY
// entry is replaced (never duplicated) so the fallback wins in the child.
func withFallbackDisplayEnv(cmd *exec.Cmd) {
	if strings.TrimSpace(os.Getenv("DISPLAY")) != "" {
		return
	}
	fb := osFallbackDisplayProbe()
	if fb == "" {
		return
	}
	env := os.Environ()
	replaced := false
	for i, kv := range env {
		if kv == "DISPLAY" || strings.HasPrefix(kv, "DISPLAY=") {
			env[i] = "DISPLAY="+fb
			replaced = true
			break
		}
	}
	if !replaced {
		env = append(env, "DISPLAY="+fb)
	}
	cmd.Env = env
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

type osMouseDragArgs struct {
	X1     *int `json:"x1"`
	Y1     *int `json:"y1"`
	X2     *int `json:"x2"`
	Y2     *int `json:"y2"`
	Button *int `json:"button,omitempty"`
	Steps  *int `json:"steps,omitempty"`
}

type osMouseScrollArgs struct {
	Direction string `json:"direction"`
	Amount    *int   `json:"amount,omitempty"`
}

type osScreenshotArgs struct {
	Display *string  `json:"display,omitempty"`
	Name    *string  `json:"name,omitempty"`
	Scale   *float64 `json:"scale,omitempty"`
}

type osWindowListArgs struct {
	Pattern *string `json:"pattern,omitempty"`
	Limit   *int    `json:"limit,omitempty"`
}

type osWindowFocusArgs struct {
	WindowID string  `json:"window_id"`
	Pattern  *string `json:"pattern,omitempty"`
}

type osWindowGeometryArgs struct {
	WindowID *string `json:"window_id,omitempty"`
}

type osWindowEntry struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
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
	display := effectiveOSDisplay()
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
	display := effectiveOSDisplay()
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
	if effectiveOSDisplay() == "" {
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
	if effectiveOSDisplay() == "" {
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
		if effectiveOSDisplay() == "" {
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
		if effectiveOSDisplay() == "" {
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

// ---- Extended OS control suite: drag, scroll, screenshot, windows ----

func requireX11Backend(tool string) (string, error) {
	backend, err := pickOSInputBackend()
	if err != nil {
		return "", err
	}
	if backend == "wtype" {
		return "", fmt.Errorf("%s unavailable: wtype supports keyboard only, install xdotool and set DISPLAY for mouse/window control", tool)
	}
	if effectiveOSDisplay() == "" {
		return "", fmt.Errorf("%s unavailable: DISPLAY is not set (xdotool requires an X11 display)", tool)
	}
	return backend, nil
}

func validOSWindowID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > maxOSInputWindowIDLen {
		return false
	}
	for _, r := range id {
		if r >= '0' && r <= '9' {
			continue
		}
		if r >= 'a' && r <= 'f' {
			continue
		}
		if r >= 'A' && r <= 'F' {
			continue
		}
		if r == 'x' || r == 'X' {
			continue
		}
		return false
	}
	return true
}

func validOSDisplay(display string) bool {
	display = strings.TrimSpace(display)
	if display == "" || len(display) > maxOSInputDisplayLen {
		return false
	}
	for _, r := range display {
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
		case ':', '.', '-', '_', '/':
			continue
		default:
			return false
		}
	}
	return true
}

func osInputRunBytes(ctx context.Context, name string, args []string) ([]byte, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	withFallbackDisplayEnv(cmd)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, strings.TrimSpace(string(exitErr.Stderr)), err
		}
		return nil, "", err
	}
	return out, "", nil
}

var osInputRunCapture = osInputRunBytes

var osInputNow = time.Now

func osMouseDragHandler(ctx context.Context, raw json.RawMessage) (string, error) {
	const tool = "os_mouse_drag"
	var args osMouseDragArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid %s arguments: %w", tool, err)
	}
	if args.X1 == nil || args.Y1 == nil || args.X2 == nil || args.Y2 == nil {
		return "", errors.New("os_mouse_drag requires x1, y1, x2 and y2")
	}
	button := 1
	if args.Button != nil {
		button = *args.Button
	}
	if button < 1 || button > 9 {
		return "", errors.New("os_mouse_drag button must be 1-9 (1=left, 2=middle, 3=right)")
	}
	steps := 10
	if args.Steps != nil {
		steps = *args.Steps
	}
	if steps < 1 || steps > maxOSInputDragSteps {
		return "", fmt.Errorf("os_mouse_drag steps must be 1-%d", maxOSInputDragSteps)
	}
	x1, y1 := clampOSInputCoord(*args.X1), clampOSInputCoord(*args.Y1)
	x2, y2 := clampOSInputCoord(*args.X2), clampOSInputCoord(*args.Y2)
	argv := []string{"mousemove", strconv.Itoa(x1), strconv.Itoa(y1),
		"mousedown", strconv.Itoa(button)}
	for i := 1; i <= steps; i++ {
		xi := x1 + (x2-x1)*i/steps
		yi := y1 + (y2-y1)*i/steps
		argv = append(argv, "mousemove", strconv.Itoa(xi), strconv.Itoa(yi))
	}
	argv = append(argv, "mouseup", strconv.Itoa(button))
	if osInputDryRun() {
		return osInputDryRunResult(intendedOSInputBackend(), "mouse_drag", argv,
			map[string]any{"x1": x1, "y1": y1, "x2": x2, "y2": y2, "button": button, "steps": steps})
	}
	if _, err := requireX11Backend(tool); err != nil {
		return "", err
	}
	out, err := osInputRun(ctx, "xdotool", argv)
	if err != nil {
		return "", fmt.Errorf("os_mouse_drag failed: %v %s", err, out)
	}
	return osInputResult(map[string]any{
		"backend": "xdotool", "action": "mouse_drag",
		"x1": x1, "y1": y1, "x2": x2, "y2": y2,
		"button": button, "steps": steps, "output": out,
	})
}

func osMouseScrollHandler(ctx context.Context, raw json.RawMessage) (string, error) {
	const tool = "os_mouse_scroll"
	var args osMouseScrollArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid %s arguments: %w", tool, err)
	}
	button := 0
	switch strings.ToLower(strings.TrimSpace(args.Direction)) {
	case "up":
		button = 4
	case "down":
		button = 5
	case "left":
		button = 6
	case "right":
		button = 7
	default:
		return "", errors.New(`os_mouse_scroll direction must be up, down, left or right`)
	}
	amount := 1
	if args.Amount != nil {
		amount = *args.Amount
	}
	if amount < 1 || amount > maxOSInputScrollAmount {
		return "", fmt.Errorf("os_mouse_scroll amount must be 1-%d", maxOSInputScrollAmount)
	}
	argv := []string{"click", "--repeat", strconv.Itoa(amount), "--delay", "10", strconv.Itoa(button)}
	if osInputDryRun() {
		return osInputDryRunResult(intendedOSInputBackend(), "mouse_scroll", argv,
			map[string]any{"direction": strings.ToLower(strings.TrimSpace(args.Direction)), "amount": amount, "button": button})
	}
	if _, err := requireX11Backend(tool); err != nil {
		return "", err
	}
	out, err := osInputRun(ctx, "xdotool", argv)
	if err != nil {
		return "", fmt.Errorf("os_mouse_scroll failed: %v %s", err, out)
	}
	return osInputResult(map[string]any{
		"backend": "xdotool", "action": "mouse_scroll",
		"direction": strings.ToLower(strings.TrimSpace(args.Direction)),
		"amount":    amount, "button": button, "output": out,
	})
}

func osScreenshotName(raw *string, now time.Time) string {
	if raw != nil {
		name := strings.TrimSpace(*raw)
		if name != "" {
			name = strings.ReplaceAll(name, "/", "-")
			name = strings.ReplaceAll(name, "\\", "-")
			if runes := []rune(name); len(runes) > maxOSInputScreenshotNameLen {
				name = string(runes[:maxOSInputScreenshotNameLen])
			}
			if !strings.HasSuffix(strings.ToLower(name), ".png") {
				name += ".png"
			}
			return name
		}
	}
	return "screenshot-" + now.UTC().Format("20060102-150405") + ".png"
}

func (r *Registry) osScreenshotHandler() handler {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		const tool = "os_screenshot"
		var args osScreenshotArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid %s arguments: %w", tool, err)
		}
		display := ""
		if args.Display != nil && strings.TrimSpace(*args.Display) != "" {
			display = strings.TrimSpace(*args.Display)
			if !validOSDisplay(display) {
				return "", errors.New("os_screenshot display contains unsupported characters (letters, digits, :, ., -, _, /; max 64 chars)")
			}
		}
		if args.Scale != nil {
			if *args.Scale <= 0 || *args.Scale > 1 {
				return "", errors.New("os_screenshot scale must be within (0, 1]")
			}
		}
		argv := []string{"-root"}
		if display != "" {
			argv = append(argv, "-display", display)
		}
		argv = append(argv, "-png")
		name := osScreenshotName(args.Name, osInputNow())
		if osInputDryRun() {
			extra := map[string]any{"name": name}
			if display != "" {
				extra["display"] = display
			}
			if args.Scale != nil {
				extra["scale"] = *args.Scale
			}
			extra["note"] = "captured PNG is stored in the session attachment store as a file reference; no bytes on the text path"
			return osInputDryRunResult("import", "screenshot", append([]string{"import"}, argv...), extra)
		}
		if _, err := osInputLookPath("import"); err != nil {
			return "", errors.New("os_screenshot unavailable: ImageMagick 'import' not found in PATH (install imagemagick for screen capture)")
		}
		data, stderr, err := osInputRunCapture(ctx, "import", argv)
		if err != nil {
			return "", fmt.Errorf("os_screenshot failed: %v %s", err, stderr)
		}
		if !bytes.HasPrefix(data, osPNGMagic) {
			return "", errors.New("os_screenshot failed: capture did not return PNG data")
		}
		store, err := r.attachmentStore()
		if err != nil {
			return "", err
		}
		sessionID, err := attachmentSession(ctx)
		if err != nil {
			return "", err
		}
		ref, err := store.PutWithContentType(ctx, sessionID, name, "image/png", bytes.NewReader(data))
		if err != nil {
			return "", fmt.Errorf("os_screenshot store failed: %w", err)
		}
		sdk.RecordOutboundAttachment(sessionID, ref.ID)
		width, height, format, dimErr := decodeImageDimensions(data)
		payload := map[string]any{
			"backend": "import", "action": "screenshot",
			"name": name, "content_type": "image/png",
			"size": ref.Size, "ref_id": ref.ID, "path": ref.Path,
			"format": format,
		}
		if dimErr == nil {
			payload["width"] = width
			payload["height"] = height
		}
		if display != "" {
			payload["display"] = display
		}
		if args.Scale != nil {
			payload["scale"] = *args.Scale
			payload["scale_note"] = "scale is advisory: resize the stored PNG after download (e.g. convert -resize) before reading"
		}
		_ = ref
		return osInputResult(payload)
	}
}

func osWindowListHandler(ctx context.Context, raw json.RawMessage) (string, error) {
	const tool = "os_window_list"
	var args osWindowListArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid %s arguments: %w", tool, err)
	}
	pattern := ""
	if args.Pattern != nil {
		pattern = strings.TrimSpace(*args.Pattern)
		if len([]rune(pattern)) > maxOSInputPatternRunes {
			return "", fmt.Errorf("os_window_list pattern exceeds %d characters", maxOSInputPatternRunes)
		}
	}
	limit := maxOSWindowListDefault
	if args.Limit != nil {
		limit = *args.Limit
	}
	if limit < 1 || limit > maxOSWindowListMax {
		return "", fmt.Errorf("os_window_list limit must be 1-%d", maxOSWindowListMax)
	}
	argv := []string{"search", "--onlyvisible", "--name", pattern}
	if pattern == "" {
		argv = []string{"search", "--onlyvisible", ".*"}
	}
	if osInputDryRun() {
		return osInputDryRunResult(intendedOSInputBackend(), "window_list", argv,
			map[string]any{"pattern": pattern, "limit": limit})
	}
	if _, err := requireX11Backend(tool); err != nil {
		return "", err
	}
	out, err := osInputRun(ctx, "xdotool", argv)
	if err != nil && strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("os_window_list failed: %v %s", err, out)
	}
	ids := strings.Fields(out)
	windows := make([]osWindowEntry, 0, len(ids))
	for _, id := range ids {
		if len(windows) >= limit {
			break
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		title := ""
		if tout, terr := osInputRun(ctx, "xdotool", []string{"getwindowname", id}); terr == nil {
			title = strings.TrimSpace(tout)
		}
		windows = append(windows, osWindowEntry{ID: id, Title: title})
	}
	return osInputResult(map[string]any{
		"backend": "xdotool", "action": "window_list",
		"pattern": pattern, "limit": limit, "count": len(windows), "windows": windows,
	})
}

func osWindowFocusHandler(ctx context.Context, raw json.RawMessage) (string, error) {
	const tool = "os_window_focus"
	var args osWindowFocusArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid %s arguments: %w", tool, err)
	}
	windowID := strings.TrimSpace(args.WindowID)
	pattern := ""
	if args.Pattern != nil {
		pattern = strings.TrimSpace(*args.Pattern)
	}
	if windowID == "" && pattern == "" {
		return "", errors.New("os_window_focus requires window_id or pattern")
	}
	if windowID != "" && !validOSWindowID(windowID) {
		return "", errors.New("os_window_focus window_id must be a decimal or 0x-prefixed hex window id (max 20 chars)")
	}
	if len([]rune(pattern)) > maxOSInputPatternRunes {
		return "", fmt.Errorf("os_window_focus pattern exceeds %d characters", maxOSInputPatternRunes)
	}
	var argv []string
	target := windowID
	if target == "" {
		target = pattern
		argv = []string{"search", "--onlyvisible", "--name", pattern, "windowactivate", "--sync"}
	} else {
		argv = []string{"windowactivate", "--sync", windowID}
	}
	if osInputDryRun() {
		extra := map[string]any{"target": target}
		if windowID != "" {
			extra["window_id"] = windowID
		}
		if pattern != "" {
			extra["pattern"] = pattern
		}
		return osInputDryRunResult(intendedOSInputBackend(), "window_focus", argv, extra)
	}
	if _, err := requireX11Backend(tool); err != nil {
		return "", err
	}
	out, err := osInputRun(ctx, "xdotool", argv)
	if err != nil {
		return "", fmt.Errorf("os_window_focus failed: %v %s", err, out)
	}
	payload := map[string]any{
		"backend": "xdotool", "action": "window_focus",
		"target": target, "output": out,
	}
	if windowID != "" {
		payload["window_id"] = windowID
	}
	if pattern != "" {
		payload["pattern"] = pattern
	}
	return osInputResult(payload)
}

func osWindowGeometryHandler(ctx context.Context, raw json.RawMessage) (string, error) {
	const tool = "os_window_geometry"
	var args osWindowGeometryArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid %s arguments: %w", tool, err)
	}
	windowID := ""
	if args.WindowID != nil {
		windowID = strings.TrimSpace(*args.WindowID)
		if !validOSWindowID(windowID) {
			return "", errors.New("os_window_geometry window_id must be a decimal or 0x-prefixed hex window id (max 20 chars)")
		}
	}
	argv := []string{"getwindowgeometry"}
	if windowID != "" {
		argv = append(argv, "--shell", windowID)
	} else {
		argv = append(argv, "--shell")
	}
	if osInputDryRun() {
		extra := map[string]any{}
		if windowID != "" {
			extra["window_id"] = windowID
		}
		return osInputDryRunResult(intendedOSInputBackend(), "window_geometry", argv, extra)
	}
	if _, err := requireX11Backend(tool); err != nil {
		return "", err
	}
	out, err := osInputRun(ctx, "xdotool", argv)
	if err != nil {
		return "", fmt.Errorf("os_window_geometry failed: %v %s", err, out)
	}
	geom := map[string]any{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		switch key {
		case "X", "Y", "WIDTH", "HEIGHT", "SCREEN":
			if n, convErr := strconv.Atoi(val); convErr == nil {
				geom[strings.ToLower(key)] = n
			}
		case "WINDOW":
			geom["window"] = val
		}
	}
	payload := map[string]any{
		"backend": "xdotool", "action": "window_geometry",
		"geometry": geom, "output": out,
	}
	if windowID != "" {
		payload["window_id"] = windowID
	}
	return osInputResult(payload)
}
