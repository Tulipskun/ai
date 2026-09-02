package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/Tulipskun/ai/sdk"
)

type handler func(context.Context, json.RawMessage) (string, error)

type Registry struct {
	workspace string
	jobs      *JobManager
	handlers  map[string]handler
	defs      []sdk.Tool
}

func NewRegistry(workspace string) (*Registry, error) { return NewRegistryWithBrowser(workspace, nil, false) }

func NewRegistryWithBrowser(workspace string, browser *BrowserClient, allowPrivate bool) (*Registry, error) {
	root, err := workspaceRoot(workspace)
	if err != nil { return nil, err }
	jobs := NewJobManager(root)
	r := &Registry{workspace: root, jobs: jobs, handlers: make(map[string]handler)}
	r.register("read_file", `Read a UTF-8 text file inside the workspace.`, readFileTool(root), map[string]any{"type":"object","properties":map[string]any{"path":map[string]any{"type":"string"}},"required":[]string{"path"}})
	r.register("write_file", `Write UTF-8 text to a file inside the workspace, creating parent directories when needed.`, writeFileTool(root), map[string]any{"type":"object","properties":map[string]any{"path":map[string]any{"type":"string"},"content":map[string]any{"type":"string"}},"required":[]string{"path","content"}})
	r.register("edit_file", `Replace exactly one occurrence of old_text with new_text in a UTF-8 file inside the workspace.`, editFileTool(root), map[string]any{"type":"object","properties":map[string]any{"path":map[string]any{"type":"string"},"old_text":map[string]any{"type":"string"},"new_text":map[string]any{"type":"string"}},"required":[]string{"path","old_text","new_text"}})
	r.register("list_directory", `List entries in a directory inside the workspace.`, listDirectoryTool(root), map[string]any{"type":"object","properties":map[string]any{"path":map[string]any{"type":"string"}}})
	r.register("search_files", `Search UTF-8 text files recursively inside the workspace.`, searchFilesTool(root), map[string]any{"type":"object","properties":map[string]any{"query":map[string]any{"type":"string"},"path":map[string]any{"type":"string"}},"required":[]string{"query"}})
	r.register("run_command", `Run a command synchronously in the workspace.`, runCommandTool(root), map[string]any{"type":"object","properties":map[string]any{"command":map[string]any{"type":"string"},"args":map[string]any{"type":"array","items":map[string]any{"type":"string"}},"timeout_ms":map[string]any{"type":"integer"}},"required":[]string{"command"}})
	r.register("run_job", `Start a long-running command in the background and return a job ID.`, runJobTool(jobs), map[string]any{"type":"object","properties":map[string]any{"command":map[string]any{"type":"string"},"args":map[string]any{"type":"array","items":map[string]any{"type":"string"}}},"required":[]string{"command"}})
	r.register("check_job", `Inspect a background job without waiting for it.`, checkJobTool(jobs), map[string]any{"type":"object","properties":map[string]any{"job_id":map[string]any{"type":"string"}},"required":[]string{"job_id"}})
	r.register("close_job", `Terminate a running background job.`, closeJobTool(jobs), map[string]any{"type":"object","properties":map[string]any{"job_id":map[string]any{"type":"string"}},"required":[]string{"job_id"}})

	policy := NewNetworkPolicy(allowPrivate)
	r.register("web_fetch", `Fetch readable content from an HTTP/HTTPS webpage or API.`, newWebFetchTool(policy), browserSchema(map[string]any{"url":stringProperty()}, []string{"url"}))
	if browser != nil {
		r.register("browser_open", `Open a browser page in the current AI session.`, newBrowserTool(browser,"browser.open",decodeJSON[browserSessionArgs]), browserSchema(map[string]any{"session_id":stringProperty()},[]string{"session_id"}))
		r.register("browser_close", `Close the browser context for the current AI session.`, newBrowserTool(browser,"browser.close",decodeJSON[browserSessionArgs]), browserSchema(map[string]any{"session_id":stringProperty()},[]string{"session_id"}))
		r.register("browser_navigate", `Navigate the current browser page to an HTTP/HTTPS URL.`, newBrowserNavigateTool(browser,policy), browserSchema(map[string]any{"session_id":stringProperty(),"url":stringProperty()},[]string{"session_id","url"}))
		r.register("browser_snapshot", `Capture the current browser accessibility snapshot with element references.`, newBrowserTool(browser,"browser.snapshot",decodeJSON[browserSessionArgs]), browserSchema(map[string]any{"session_id":stringProperty()},[]string{"session_id"}))
		r.register("browser_click", `Click an element identified by a current browser snapshot reference.`, newBrowserTool(browser,"browser.click",decodeJSON[browserRefArgs]), browserSchema(map[string]any{"session_id":stringProperty(),"ref":stringProperty()},[]string{"session_id","ref"}))
		r.register("browser_fill", `Fill an input identified by a current browser snapshot reference.`, newBrowserTool(browser,"browser.fill",decodeJSON[browserFillArgs]), browserSchema(map[string]any{"session_id":stringProperty(),"ref":stringProperty(),"text":stringProperty()},[]string{"session_id","ref","text"}))
		r.register("browser_press", `Press a keyboard key on an element identified by a current browser snapshot reference.`, newBrowserTool(browser,"browser.press",decodeJSON[browserPressArgs]), browserSchema(map[string]any{"session_id":stringProperty(),"ref":stringProperty(),"key":stringProperty()},[]string{"session_id","ref","key"}))
		r.register("browser_select", `Select an option on a select element identified by a current browser snapshot reference.`, newBrowserTool(browser,"browser.select",decodeJSON[browserSelectArgs]), browserSchema(map[string]any{"session_id":stringProperty(),"ref":stringProperty(),"value":stringProperty()},[]string{"session_id","ref","value"}))
		r.register("browser_scroll", `Scroll the current browser page.`, newBrowserTool(browser,"browser.scroll",decodeJSON[browserScrollArgs]), browserSchema(map[string]any{"session_id":stringProperty(),"direction":stringProperty(),"amount":intProperty()},[]string{"session_id"}))
		r.register("browser_get_text", `Extract text from the current browser page or a snapshot-referenced element.`, newBrowserTool(browser,"browser.get_text",decodeJSON[browserTextArgs]), browserSchema(map[string]any{"session_id":stringProperty(),"ref":stringProperty()},[]string{"session_id"}))
		r.register("browser_screenshot", `Capture a PNG screenshot of the current browser page.`, newBrowserTool(browser,"browser.screenshot",decodeJSON[browserScreenshotArgs]), browserSchema(map[string]any{"session_id":stringProperty(),"full_page":boolProperty()},[]string{"session_id"}))
	}
	return r,nil
}

func (r *Registry) register(name, description string, fn handler, schema any) { r.handlers[name]=fn; r.defs=append(r.defs,sdk.Tool{Name:name,Description:description,InputSchema:schema}) }
func (r *Registry) Definitions() []sdk.Tool { out:=append([]sdk.Tool(nil),r.defs...); sort.Slice(out,func(i,j int)bool{return out[i].Name<out[j].Name}); return out }
func (r *Registry) Execute(ctx context.Context, call sdk.ToolCall) sdk.ToolResult { result:=sdk.ToolResult{ID:call.ID}; fn,ok:=r.handlers[call.Name]; if !ok { result.Content=fmt.Sprintf("unknown tool: %s",call.Name); result.IsError=true; return result }; if call.Arguments=="" { call.Arguments="{}" }; if !json.Valid([]byte(call.Arguments)) { result.Content="invalid tool arguments JSON"; result.IsError=true; return result }; content,err:=fn(ctx,json.RawMessage(call.Arguments)); if err!=nil { result.Content=err.Error(); result.IsError=true; return result }; result.Content=content; return result }
func workspaceRoot(workspace string)(string,error){if workspace==""{return "",errors.New("tools: workspace is required")};return filepathAbsClean(workspace)}
