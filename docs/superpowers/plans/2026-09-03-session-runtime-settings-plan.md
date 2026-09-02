# Session Runtime Settings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make model, temperature, and thinking level first-class session-scoped settings while keeping API credentials in the provider/key-pool layer and allowing request-level overrides.

**Architecture:** Reuse the existing `SessionConfig` as the persisted session configuration instead of introducing a second settings store. Add explicit mutation/clear methods on `Session`, persist those changes through `SessionDB`, and resolve request overrides before session defaults. API keys remain represented by the session's key-pool index, never by a raw key in session settings.

**Tech Stack:** Go 1.25, existing SDK Session/SessionDB/RouterClient APIs, SQLite.

**Spec:** Approved session runtime settings architecture from the 2026-09-03 design discussion.

## Global Constraints

- Model, temperature, and thinking level are session-scoped defaults.
- Request-level values override session values for that request.
- API keys are provider/key-pool credentials; sessions select a key-pool index and never store raw API key material in settings.
- Session setting changes persist when the session uses `SessionDB`.
- Changing settings does not modify conversation history.
- Model changes do not carry provider-native reasoning state across incompatible models; existing history remains intact and provider routing decides what is sent.
- Existing public APIs remain source-compatible where practical; do not replace `SessionConfig` with a breaking new configuration type.

---

### Task 1: Add session setting mutation APIs

**Files:**
- Create: `sdk/session_settings_test.go`
- Modify: `sdk/routing.go`

- [ ] Write failing tests for setting model, temperature, thinking level, clearing optional values, and key-index selection without exposing raw keys.
- [ ] Run `go test ./sdk -run 'TestSession.*Setting|TestSession.*Key' -v` and verify failure.
- [ ] Implement `Session` setters/clearers with locking and validation; preserve history unchanged.
- [ ] Run focused tests and verify pass.
- [ ] Commit `feat: add session runtime setting APIs`.

### Task 2: Persist session setting changes

**Files:**
- Create: `sdk/session_settings_db_test.go`
- Modify: `sdk/session_db.go`
- Modify: `sdk/routing.go`

- [ ] Write failing tests proving updates made on an opened session survive close/reopen.
- [ ] Run the focused persistence tests and verify failure.
- [ ] Implement atomic session-config persistence and wire setters to it without changing turn persistence.
- [ ] Run focused persistence tests and verify pass.
- [ ] Commit `feat: persist session runtime settings`.

### Task 3: Resolve request-over-session settings

**Files:**
- Create: `sdk/session_settings_resolution_test.go`
- Modify: `sdk/router_client.go`

- [ ] Write failing tests proving request model/temperature/thinking override session settings, while omitted request values inherit session settings.
- [ ] Run focused tests and verify failure.
- [ ] Implement a small resolver used by Generate and Stream; keep provider/key selection separate.
- [ ] Run focused tests and verify pass.
- [ ] Commit `feat: resolve request settings over session defaults`.

### Task 4: Verify the complete SDK surface

**Files:**
- Modify: `README.md` if the existing session API documentation needs updating.

- [ ] Run `go test ./... -timeout 2m`.
- [ ] Run `go vet ./...`.
- [ ] Inspect the diff for raw API-key exposure, history mutation, or breaking API changes.
- [ ] If documentation is needed, update it and commit separately.
