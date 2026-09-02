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

func NewRegistry(workspace string) (*Registry, error) {
	root, err := workspaceRoot(workspace)
	if err != nil {
		return nil, err
	}
	jobs := NewJobManager(root)
	r := &Registry{workspace: root, jobs: jobs, handlers: make(map[string]handler)}
	r.register("read_file", `Read a UTF-8 text file inside the workspace.`, readFileTool(root), map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}})
	r.register("write_file", `Write UTF-8 text to a file inside the workspace, creating parent directories when needed.`, writeFileTool(root), map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}}, "required": []string{"path", "content"}})
	r.register("edit_file", `Replace exactly one occurrence of old_text with new_text in a UTF-8 file inside the workspace.`, editFileTool(root), map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "old_text": map[string]any{"type": "string"}, "new_text": map[string]any{"type": "string"}}, "required": []string{"path", "old_text", "new_text"}})
	r.register("list_directory", `List entries in a directory inside the workspace.`, listDirectoryTool(root), map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}})
	r.register("search_files", `Search UTF-8 text files recursively inside the workspace.`, searchFilesTool(root), map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}}, "required": []string{"query"}})
	r.register("run_command", `Run a command synchronously in the workspace.`, runCommandTool(root), map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}, "args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "timeout_ms": map[string]any{"type": "integer"}}, "required": []string{"command"}})
	r.register("run_job", `Start a long-running command in the background and return a job ID.`, runJobTool(jobs), map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}, "args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, "required": []string{"command"}})
	r.register("check_job", `Inspect a background job without waiting for it.`, checkJobTool(jobs), map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}}, "required": []string{"job_id"}})
	r.register("close_job", `Terminate a running background job.`, closeJobTool(jobs), map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}}, "required": []string{"job_id"}})
	return r, nil
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
