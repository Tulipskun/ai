# Agent Loop and Coding Tools Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a provider-neutral agent loop plus safe coding tools and background job controls.

**Architecture:** `sdk.Agent` owns the model/tool loop and `sdk.ToolExecutor` isolates tool implementations. `tools` owns filesystem, command, and background-job behavior. `HarnessLoop` delegates turn execution to `Agent` when present while retaining its existing input/display responsibilities.

**Tech Stack:** Go, standard library, existing `sdk.RouterClient`, existing `sdk.Session`.

**Spec:** `docs/superpowers/specs/2026-09-02-agent-loop-tools-design.md`

## Global Constraints

- Do not add a time tool.
- Tool failures must become error tool results.
- File paths must remain under the configured workspace root.
- `edit_file` uses exact one-occurrence replacement.
- `run_job` is asynchronous; `check_job` does not wait; `close_job` terminates running jobs.
- Job state is process-local and in-memory.
- Display remains asynchronous.
- Agent iterations are hard-bounded.

---

### Task 1: Agent tool contract and loop

**Files:**
- Create: `sdk/agent.go`
- Create: `sdk/agent_test.go`

**Interfaces:**
- `ToolExecutor.Definitions() []Tool`
- `ToolExecutor.Execute(context.Context, ToolCall) ToolResult`
- `Agent.RunTurn(context.Context, *Session, Turn, Request) (Response, error)`

- [ ] **Step 1: Write failing tests** for a final response, one tool call followed by a final response, unknown tool, tool error, and max iterations.
- [ ] **Step 2: Run `go test ./sdk -run Agent` and verify the tests fail because the agent types do not exist.**
- [ ] **Step 3: Implement the minimal loop: append user turn, request model with registered definitions, commit model response/tool calls, execute calls, append tool results, repeat, restore history on provider failure, and stop at `MaxIterations`.**
- [ ] **Step 4: Run `go test ./sdk -run Agent` and verify PASS.**
- [ ] **Step 5: Commit `feat: add sdk agent loop`.**

### Task 2: Harness integration

**Files:**
- Modify: `sdk/loop.go`
- Create/modify: `sdk/loop_test.go`

**Interfaces:**
- Add optional `Agent *Agent` to `HarnessLoop`.
- When `Agent` is non-nil, `Handle` calls `Agent.RunTurn`; otherwise preserve `RouterClient.GenerateTurn` compatibility.

- [ ] **Step 1: Add a failing test proving the HarnessLoop uses Agent when configured.**
- [ ] **Step 2: Run the focused test and verify failure.**
- [ ] **Step 3: Add the optional Agent field and branch only the turn execution path.**
- [ ] **Step 4: Run focused tests and `go test ./sdk`.**
- [ ] **Step 5: Commit `feat: integrate agent loop with harness`.**

### Task 3: Coding tool registry

**Files:**
- Create: `tools/registry.go`
- Create: `tools/registry_test.go`

**Interfaces:**
- `Registry` implements `sdk.ToolExecutor`.
- `NewRegistry(workspace string) (*Registry, error)` registers the coding tools.

- [ ] **Step 1: Write failing registry and definition tests.**
- [ ] **Step 2: Run focused tests and verify failure.**
- [ ] **Step 3: Implement registration and JSON argument dispatch.**
- [ ] **Step 4: Run focused tests and verify PASS.**
- [ ] **Step 5: Commit `feat: add coding tool registry`.**

### Task 4: File tools

**Files:**
- Create: `tools/files.go`
- Create: `tools/files_test.go`

**Interfaces:**
- `read_file(path)`
- `write_file(path, content)`
- `edit_file(path, old_text, new_text)`
- `list_directory(path)`
- `search_files(query, path)`

- [ ] **Step 1: Write failing tests for normal access, traversal rejection, exact edit, missing old text, and ambiguous edit.**
- [ ] **Step 2: Run focused tests and verify failure.**
- [ ] **Step 3: Implement workspace-root path validation and the five tools.**
- [ ] **Step 4: Run focused tests and verify PASS.**
- [ ] **Step 5: Commit `feat: add coding file tools`.**

### Task 5: Command and background jobs

**Files:**
- Create: `tools/command.go`
- Create: `tools/jobs.go`
- Create: `tools/jobs_test.go`

**Interfaces:**
- `run_command(command, args, timeout_ms)` waits for completion.
- `run_job(command, args)` starts asynchronously and returns `{job_id}`.
- `check_job(job_id)` returns state/output/exit code.
- `close_job(job_id)` terminates a running process.

- [ ] **Step 1: Write failing tests for command success/failure, job completion/failure, and closing a running job.**
- [ ] **Step 2: Run focused tests and verify failure.**
- [ ] **Step 3: Implement bounded output capture, process lifecycle tracking, and context-aware termination.**
- [ ] **Step 4: Run focused tests and verify PASS.**
- [ ] **Step 5: Commit `feat: add command and background job tools`.**

### Task 6: Documentation and verification

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Document Agent construction, tool registry, and the `run_job/check_job/close_job` lifecycle.**
- [ ] **Step 2: Run `gofmt` on changed Go files.**
- [ ] **Step 3: Run `go test ./...`.**
- [ ] **Step 4: Run `go vet ./...`.**
- [ ] **Step 5: Check the GitHub Actions result for the final commit before claiming completion.**
- [ ] **Step 6: Commit `docs: document agent loop and coding tools`.**
