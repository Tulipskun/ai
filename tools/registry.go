package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Tulipskun/ai/sdk"
)

type handler func(context.Context, json.RawMessage) (string, error)

type Registry struct {
	workspace    string
	resolver     func(context.Context) string
	jobs         *JobManager
	handlers     map[string]handler
	defs         []sdk.Tool
	attachments  AttachmentStore
	attachmentMu sync.RWMutex
}

func NewRegistry(workspace string) (*Registry, error) {
	return NewRegistryWithBrowser(workspace, nil, false, "")
}

func NewRegistryWithBrowser(workspace string, browser *BrowserClient, allowPrivate bool, jobsPath string) (*Registry, error) {
	root, err := workspaceRoot(workspace)
	if err != nil {
		return nil, err
	}
	if jobsPath == "" {
		jobsPath = filepath.Join(root, "data", "jobs.json")
	}
	jobs := NewJobManager(root, jobsPath)
	r := &Registry{workspace: root, jobs: jobs, handlers: make(map[string]handler)}
	r.register("read_file", `Read a UTF-8 text file inside the workspace.`, readFileTool(r.rootFor), map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}})
	r.register("read_files", `Read up to 32 UTF-8 text files in one call. Default for multi-file reads: batch needed files in one call instead of repeated read_file or discovery loops. Same safePath/withinRoot discipline, per-file and total byte caps, one JSON array result with path/bytes/truncated/content per file. Start repository work by reading index.md first.`, readFilesTool(r.rootFor), map[string]any{"type": "object", "properties": map[string]any{"paths": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "max_bytes": map[string]any{"type": "integer"}, "max_total_bytes": map[string]any{"type": "integer"}}, "required": []string{"paths"}})
	r.register("write_file", `Write UTF-8 text to a file inside the workspace, creating parent directories when needed.`, writeFileTool(r.rootFor), map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}}, "required": []string{"path", "content"}})
	r.register("edit_file", `Replace exactly one occurrence of old_text with new_text in a UTF-8 file inside the workspace.`, editFileTool(r.rootFor), map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "old_text": map[string]any{"type": "string"}, "new_text": map[string]any{"type": "string"}}, "required": []string{"path", "old_text", "new_text"}})
	r.register("list_directory", `List entries in a directory inside the workspace.`, listDirectoryTool(r.rootFor), map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}})
	r.register("search_files", `Search UTF-8 text files recursively inside the workspace.`, searchFilesTool(r.rootFor), map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}}, "required": []string{"query"}})
	r.register("bash", `Run a bash command line synchronously in the workspace. Type shell exactly as you would in a terminal: chains (&&, ||, ;), pipes, redirects, globs, quoting and multi-line all work. Returns combined stdout/stderr and the exit code.`, bashTool(r.rootFor), map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}, "timeout_ms": map[string]any{"type": "integer"}}, "required": []string{"command"}})
	r.register("run_job", `Start a long-running command in the background and return a job ID owned by the current session.`, runJobTool(jobs, r.rootFor), map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}, "args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, "required": []string{"command"}})
	r.register("check_job", `Inspect a background job owned by the current session without waiting for it.`, checkJobTool(jobs), map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}}, "required": []string{"job_id"}})
	r.register("close_job", `Terminate a running background job owned by the current session.`, closeJobTool(jobs), map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}}, "required": []string{"job_id"}})

	policy := NewNetworkPolicy(allowPrivate)
	r.register("web_fetch", `Fetch readable content from an HTTP/HTTPS webpage or API.`, newWebFetchTool(policy), browserSchema(map[string]any{"url": stringProperty()}, []string{"url"}))
	r.register("list_attachments", `List the attachment file references stored for the current session: id, name, content type, size, and relative path inside the attachment store. Returns no file content.`, r.listAttachmentsHandler(), browserSchema(nil, nil))
	r.register("read_attachment", `Read the text content of one attachment owned by the current session, addressed by ref_id from list_attachments. Text-like content only, size-bounded with a truncated flag; binary files are refused and described instead.`, r.readAttachmentHandler(), browserSchema(map[string]any{"ref_id": stringProperty(), "max_bytes": intProperty()}, []string{"ref_id"}))
	r.register("describe_attachment", `Describe one attachment owned by the current session: size, content type, PNG/JPEG/GIF dimensions, and for PDFs a page count plus best-effort extracted text. Metadata only; never returns raw bytes.`, r.describeAttachmentHandler(), browserSchema(map[string]any{"ref_id": stringProperty(), "max_bytes": intProperty()}, []string{"ref_id"}))
	r.register("send_attachment", `Queue one attachment owned by the current session for upload to the originating channel, addressed by ref_id from list_attachments. Validates the reference against the session store and records an upload intent; returns a short confirmation only, never file bytes.`, r.sendAttachmentHandler(), browserSchema(map[string]any{"ref_id": stringProperty()}, []string{"ref_id"}))
	r.register("os_mouse_move", `Move the OS mouse pointer to absolute screen coordinates x and y (clamped to 0-16384). X11 via xdotool (requires DISPLAY); unavailable under keyboard-only wtype sessions.`, osMouseMoveHandler, browserSchema(map[string]any{"x": intProperty(), "y": intProperty()}, []string{"x", "y"}))
	r.register("os_mouse_click", `Click an OS mouse button (1=left, 2=middle, 3=right, default 1); optionally moves to x and y first (both required when moving). X11 via xdotool (requires DISPLAY); unavailable under keyboard-only wtype sessions.`, osMouseClickHandler, browserSchema(map[string]any{"button": intProperty(), "x": intProperty(), "y": intProperty()}, nil))
	r.register("os_key_press", `Press an OS key like Return, Tab, Escape, or ctrl+c (letters, digits, _, +, -, .; max 64 chars). X11 via xdotool key, Wayland via wtype -k per +-separated part.`, osKeyPressHandler, browserSchema(map[string]any{"key": stringProperty()}, []string{"key"}))
	r.register("os_type_text", `Type non-empty OS text (max 4000 runes) at the focused window. X11 via xdotool type --clearmodifiers, Wayland via wtype.`, osTypeTextHandler, browserSchema(map[string]any{"text": stringProperty()}, []string{"text"}))
	r.register("os_screenshot", `Capture the whole screen to a PNG stored in the session attachment store (reference only: name, content_type, size, ref_id, path; never bytes on the text path) and queued for upload. X11 via ImageMagick import -root -png (optional display like :0, optional name, optional advisory scale within (0,1] with a resize note).`, r.osScreenshotHandler(), browserSchema(map[string]any{"display": stringProperty(), "name": stringProperty(), "scale": map[string]any{"type": "number"}}, nil))
	r.register("os_mouse_drag", `Drag the OS mouse holding a button (1=left default, 2-9) from x1,y1 to x2,y2 with 1-50 interpolation steps (default 10) via xdotool mousedown/mousemove/mouseup. Coords clamped 0-16384. X11 only.`, osMouseDragHandler, browserSchema(map[string]any{"x1": intProperty(), "y1": intProperty(), "x2": intProperty(), "y2": intProperty(), "button": intProperty(), "steps": intProperty()}, []string{"x1", "y1", "x2", "y2"}))
	r.register("os_mouse_scroll", `Scroll with the OS mouse wheel via xdotool wheel clicks (up=button 4, down=5, left=6, right=7), 1-20 repeats (default 1). X11 only.`, osMouseScrollHandler, browserSchema(map[string]any{"direction": stringProperty(), "amount": intProperty()}, []string{"direction"}))
	r.register("os_window_list", `List visible X11 windows via xdotool search (optional name pattern substring, max 256 chars; optional limit 1-200 default 50) with window id and title each. X11 only.`, osWindowListHandler, browserSchema(map[string]any{"pattern": stringProperty(), "limit": intProperty()}, nil))
	r.register("os_window_focus", `Focus an X11 window via xdotool windowactivate --sync using a window_id (decimal or 0x hex, max 20 chars) or a name pattern (max 256 chars); one of the two is required. X11 only.`, osWindowFocusHandler, browserSchema(map[string]any{"window_id": stringProperty(), "pattern": stringProperty()}, nil))
	r.register("os_window_geometry", `Report X11 window geometry via xdotool getwindowgeometry --shell (optional window_id, defaults to the active window) with x/y/width/height/screen plus raw output. X11 only.`, osWindowGeometryHandler, browserSchema(map[string]any{"window_id": stringProperty()}, nil))
	if browser != nil {
		r.register("browser_list_pages", `List browser tabs currently available through the configured browser CDP endpoint.`, newBrowserListPagesTool(browser), browserSchema(nil, nil))
		r.register("browser_attach", `Attach the current AI browser session to an existing browser tab by target_id.`, newBrowserAttachTool(browser), browserSchema(map[string]any{"session_id": stringProperty(), "target_id": stringProperty()}, []string{"session_id", "target_id"}))
		r.register("browser_open", `Open a new browser page in the current AI session.`, newBrowserTool(browser, "browser.open", decodeJSON[browserSessionArgs]), browserSchema(map[string]any{"session_id": stringProperty()}, []string{"session_id"}))
		r.register("browser_close", `Close the browser page for the current AI session.`, newBrowserTool(browser, "browser.close", decodeJSON[browserSessionArgs]), browserSchema(map[string]any{"session_id": stringProperty()}, []string{"session_id"}))
		r.register("browser_navigate", `Navigate the current browser page to an HTTP/HTTPS URL.`, newBrowserNavigateTool(browser, policy), browserSchema(map[string]any{"session_id": stringProperty(), "url": stringProperty()}, []string{"session_id", "url"}))
		r.register("browser_snapshot", `Capture the current browser accessibility snapshot with element references.`, newBrowserTool(browser, "browser.snapshot", decodeJSON[browserSessionArgs]), browserSchema(map[string]any{"session_id": stringProperty()}, []string{"session_id"}))
		r.register("browser_click", `Click an element identified by a current browser snapshot reference.`, newBrowserTool(browser, "browser.click", decodeJSON[browserRefArgs]), browserSchema(map[string]any{"session_id": stringProperty(), "ref": stringProperty()}, []string{"session_id", "ref"}))
		r.register("browser_fill", `Fill an input identified by a current browser snapshot reference.`, newBrowserTool(browser, "browser.fill", decodeJSON[browserFillArgs]), browserSchema(map[string]any{"session_id": stringProperty(), "ref": stringProperty(), "text": stringProperty()}, []string{"session_id", "ref", "text"}))
		r.register("browser_press", `Press a keyboard key on an element identified by a current browser snapshot reference.`, newBrowserTool(browser, "browser.press", decodeJSON[browserPressArgs]), browserSchema(map[string]any{"session_id": stringProperty(), "ref": stringProperty(), "key": stringProperty()}, []string{"session_id", "ref", "key"}))
		r.register("browser_select", `Select an option on a select element identified by a current browser snapshot reference.`, newBrowserTool(browser, "browser.select", decodeJSON[browserSelectArgs]), browserSchema(map[string]any{"session_id": stringProperty(), "ref": stringProperty(), "value": stringProperty()}, []string{"session_id", "ref", "value"}))
		r.register("browser_scroll", `Scroll the current browser page.`, newBrowserTool(browser, "browser.scroll", decodeJSON[browserScrollArgs]), browserSchema(map[string]any{"session_id": stringProperty(), "direction": stringProperty(), "amount": intProperty()}, []string{"session_id"}))
		r.register("browser_get_text", `Extract text from the current browser page or a snapshot-referenced element.`, newBrowserTool(browser, "browser.get_text", decodeJSON[browserTextArgs]), browserSchema(map[string]any{"session_id": stringProperty(), "ref": stringProperty()}, []string{"session_id"}))
		r.register("browser_screenshot", `Capture a PNG screenshot of the current browser page.`, newBrowserTool(browser, "browser.screenshot", decodeJSON[browserScreenshotArgs]), browserSchema(map[string]any{"session_id": stringProperty(), "full_page": boolProperty()}, []string{"session_id"}))
	}
	return r, nil
}

// SetWorkspaceResolver installs a per-invocation workspace lookup used by
// every path-sensitive tool. A session with its own workspace (REQ-038)
// resolves through ctx; anything else keeps the process default.
func (r *Registry) SetWorkspaceResolver(resolver func(context.Context) string) {
	if r != nil {
		r.resolver = resolver
	}
}

func (r *Registry) rootFor(ctx context.Context) string {
	if ctx != nil {
		if ws := sdk.WorkspaceFromContext(ctx); ws != "" {
			if cleaned, err := filepathAbsClean(ws); err == nil {
				return cleaned
			}
		}
	}
	if r != nil && r.resolver != nil {
		if root := strings.TrimSpace(r.resolver(ctx)); root != "" {
			if cleaned, err := filepathAbsClean(root); err == nil {
				return cleaned
			}
		}
	}
	return r.workspace
}

func (r *Registry) register(name, description string, fn handler, schema any) {
	r.handlers[name] = fn
	r.defs = append(r.defs, sdk.Tool{Name: name, Description: description, InputSchema: schema})
}
func (r *Registry) Definitions() []sdk.Tool {
	out := append([]sdk.Tool(nil), r.defs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (r *Registry) Execute(ctx context.Context, call sdk.ToolCall) sdk.ToolResult {
	result := sdk.ToolResult{ID: call.ID}
	fn, ok := r.handlers[call.Name]
	if !ok {
		result.Content = fmt.Sprintf("unknown tool: %s", call.Name)
		result.IsError = true
		return result
	}
	if call.Arguments == "" {
		call.Arguments = "{}"
	}
	if !json.Valid([]byte(call.Arguments)) {
		result.Content = "invalid tool arguments JSON"
		result.IsError = true
		return result
	}
	content, err := fn(ctx, json.RawMessage(call.Arguments))
	if err != nil {
		result.Content = err.Error()
		result.IsError = true
		return result
	}
	result.Content = content
	return result
}
func workspaceRoot(workspace string) (string, error) {
	if workspace == "" {
		return "", errors.New("tools: workspace is required")
	}
	return filepathAbsClean(workspace)
}
