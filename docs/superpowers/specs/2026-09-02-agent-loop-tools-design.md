# Agent Loop and Coding Tools Design

## Goal
Add a provider-neutral agent loop to the SDK and a small coding-agent toolset, including background jobs for long-running commands.

## Requirements

- Do not provide a `time` tool. Each request may carry its own timestamp metadata/context.
- The agent loop must repeatedly call the model, execute returned tool calls, append tool results to session history, and continue until the model returns no tool calls or the configured iteration limit is reached.
- Tool execution is isolated from the provider layer through a registry/executor interface.
- Tool failures become `tool_result` entries with `IsError=true` so the model can recover.
- Unknown tools also become error tool results rather than crashing the loop.
- The loop must preserve session history transactionally when the initial model request fails.
- Coding tools must include file read, file write, file edit, directory listing, file search, foreground command execution, and background job management.
- File tools operate relative to a configured workspace root and reject paths escaping that root.
- `edit_file` performs deterministic exact-text replacement and reports an error when the expected old text is absent or ambiguous.
- `run_job` starts a command asynchronously and immediately returns a job ID.
- `check_job` returns job state and captured output without waiting for completion.
- `close_job` terminates a running job and records it as closed.
- Background jobs are process-local and in-memory for this first implementation; no persistence or distributed worker system is required.
- Job output is bounded in memory.
- Command execution has context cancellation and configurable workspace root, but no unrestricted filesystem path access.
- The existing HarnessLoop remains the transport/input/output loop; it delegates turn execution to the Agent when configured.
- Display remains asynchronous and must never block the main loop.

## Architecture

```text
InputSource
    ↓
HarnessLoop
    ↓
Agent.RunTurn
    ↓
RouterClient
    ↓
Provider
    ↓
Model response
    ↓
Tool calls?
 ┌──┴───────┐
 no        yes
 ↓          ↓
final    ToolRegistry
             ↓
         ToolExecutor
             ↓
         ToolResult
             ↓
         Session History
             ↓
           Model
             ↺
```

The SDK owns the generic loop and tool contracts. The `tools` package owns coding-specific behavior. The agent loop does not know how a file is edited or how a command is executed.

## Core contracts

```go
type ToolExecutor interface {
    Definitions() []Tool
    Execute(context.Context, ToolCall) ToolResult
}

type Agent struct {
    Client *RouterClient
    Tools ToolExecutor
    MaxIterations int
}

func (a *Agent) RunTurn(context.Context, *Session, Turn, Request) (Response, error)
```

## Coding tools

Names:

- `read_file`
- `write_file`
- `edit_file`
- `list_directory`
- `search_files`
- `run_command`
- `run_job`
- `check_job`
- `close_job`

`run_command` waits for completion. `run_job` is explicitly asynchronous and is intended for builds, tests, servers, watchers, and other commands that can outlive a model request.

## Background jobs

A job has an ID, command, working directory, PID when available, state, start/end timestamps, bounded stdout/stderr, exit code, and close/error information.

States are `running`, `completed`, `failed`, and `closed`.

`check_job` is read-only. `close_job` is idempotent for already-finished jobs and terminates only running jobs.

## Error and safety behavior

- Tool argument JSON is decoded and validated before execution.
- File paths are normalized and must remain under the workspace root.
- `edit_file` requires exactly one occurrence of the old text unless an explicit replacement count is supplied; the initial tool schema uses exactly-one semantics.
- Command tools run with an explicit workspace root as their working directory.
- Job output is capped to prevent an unbounded process from exhausting memory.
- Agent iteration count is hard-bounded.
- Provider retry behavior remains the existing RouterClient responsibility.

## Testing

Tests cover: no-tool final response, multi-step tool loop, unknown tool, tool failure, max-iteration protection, transactional session behavior, path traversal rejection, exact edit semantics, command execution, asynchronous job completion, job failure, and job closure.
