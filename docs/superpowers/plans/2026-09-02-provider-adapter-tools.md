# Provider Adapter and Runtime Tools Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make runtime provider configuration explicitly select an adapter and make the runnable AI process expose and execute the existing tool registry through the Agent loop.

**Architecture:** `provider.json` gains an explicit `adapter` field, while legacy well-known provider names continue to infer an adapter when the field is omitted. `cmd/ai` constructs the existing `tools.Registry`, injects it into `sdk.Agent`, and gives that Agent to `HarnessLoop`, so tool definitions reach the model and returned tool calls are executed until a final response is produced.

**Tech Stack:** Go 1.25, existing SDK RouterClient/Agent/HarnessLoop, existing `tools.Registry`, JSON provider configuration, Go tests and vet.

**Spec:** Approved chat design: explicit provider adapter plus Agent-backed tool execution in the runtime.

## Global Constraints

- Preserve the transport-neutral SDK boundary.
- Do not put Discord-specific or provider-specific tool logic into `sdk`.
- Preserve legacy adapter inference for existing known provider names.
- Never commit API keys or Discord tokens.
- Use TDD for behavior changes: failing tests before production implementation.
- Verify with `go test ./...` and `go vet ./...`.

---

### Task 1: Explicit Provider Adapter Configuration

**Files:**
- Modify: `runtime/provider_config.go`
- Test: `runtime/provider_config_test.go`
- Modify: `.config/provider.example.json`
- Modify: `README.md`

**Interfaces:**
- `ProviderFile` gains `Adapter string`.
- `ProviderConfigs()` uses the explicit adapter when supplied and falls back to legacy name inference when omitted.

- [ ] Write tests for `B.ai` with `adapter: openai` resolving to `sdk.AdapterOpenAI`.
- [ ] Write tests for unknown explicit adapters returning a clear error.
- [ ] Write tests that legacy `openrouter`, `opencode`, and `gemini` names still infer their existing adapters.
- [ ] Run the focused runtime tests and verify the new tests fail before implementation.
- [ ] Implement adapter parsing/validation with normalized values.
- [ ] Run focused tests and verify they pass.
- [ ] Update the provider example and README to document explicit adapters.
- [ ] Commit the provider configuration change.

### Task 2: Wire Tool Registry into the Runnable Agent

**Files:**
- Modify: `cmd/ai/main.go`
- Test: `cmd/ai/main_test.go` if helper extraction is needed for testability

**Interfaces:**
- Runtime creates one `tools.Registry` for the configured workspace.
- `sdk.Agent{Client: rt.Client, Tools: registry}` becomes the execution engine used by `HarnessLoop`.
- `HarnessLoop` continues to own transport input/output and session serialization.

- [ ] Add a failing test for the runtime wiring/helper that constructs an Agent with a non-nil ToolExecutor.
- [ ] Run the focused test and verify it fails for the missing wiring.
- [ ] Implement workspace configuration and Registry construction without exposing transport-specific details to the SDK.
- [ ] Set `HarnessLoop.Agent` and stop using the direct `Client` path for `cmd/ai` turns.
- [ ] Keep `BuildRequest` responsible only for request-level settings; Agent supplies tool definitions from the registry.
- [ ] Run focused tests and verify they pass.

### Task 3: End-to-End Tool Calling Regression Coverage

**Files:**
- Modify/create: `sdk/*_test.go` only where existing Agent tests do not cover the integration boundary.
- Modify: `README.md` if runtime setup needs clarification.

- [ ] Add or strengthen a regression test proving tool definitions are present in the model request.
- [ ] Add or strengthen a regression test proving a returned tool call is executed and its result is sent back to the model.
- [ ] Verify the final response is returned after the tool round-trip.
- [ ] Run `go test ./...`.
- [ ] Run `go vet ./...`.
- [ ] Inspect the final diff for secrets and unintended transport coupling.
- [ ] Commit the completed runtime/tool integration.

### Task 4: Integration Verification

**Files:**
- No production files unless verification reveals a defect.

- [ ] Run the full test suite.
- [ ] Run vet.
- [ ] Verify the GitHub Actions workflow for the branch is green.
- [ ] Merge the branch into `main` only after verification passes.
- [ ] Report the exact commit/merge state and the local commands needed to run B.ai + Discord.
