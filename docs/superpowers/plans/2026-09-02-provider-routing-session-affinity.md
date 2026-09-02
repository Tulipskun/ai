# Provider Routing & Session Affinity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add explicit logical-provider routing, adapter selection, and per-session API-key affinity to the canonical Go SDK.

**Architecture:** A request identifies a logical provider and model. A route resolves that pair to an underlying adapter; sessions pin a credential slot independently, so concurrent sessions do not share a mutable global key cursor. Provider adapters remain responsible for wire-format translation.

**Tech Stack:** Go 1.23, standard library, existing canonical SDK and provider adapters.

**Spec:** `docs/superpowers/specs/2026-09-02-canonical-ai-sdk-design.md`

## Global Constraints

- Keep the SDK dependency-free.
- Keep canonical conversation/tool schemas provider-neutral.
- Logical provider and underlying adapter are distinct concepts.
- API keys are scoped to logical providers and may be pinned per session.
- Do not rotate a session onto another key unless explicitly requested by the session's retry policy.

---

### Task 1: Add routing/session types

**Files:** `sdk/types.go`, `sdk/routing.go`, `sdk/routing_test.go`

- [x] Add explicit `ProviderID`, `AdapterID`, `ModelRoute`, and `SessionConfig`.
- [x] Add deterministic `Router.Register` / `Router.Resolve`.
- [x] Add session-pinned indexed key access.

### Task 2: Make key pools provider-scoped and session-selectable

**Files:** `sdk/keys.go`, `sdk/keys_test.go`

- [x] Add indexed access without moving the shared rotation cursor.
- [x] Preserve sequential rotation behavior for future retry policy integration.

### Task 3: Add route-aware client dispatch

**Files:** `sdk/router_client.go`, `sdk/router_client_test.go`

- [x] Dispatch `openrouter/gpt-5 -> openai`.
- [x] Dispatch `openrouter/gemini-3.5 -> gemini`.
- [x] Dispatch `opencode/opus -> anthropic`.
- [x] Require an explicit logical provider.

### Task 4: Wire provider clients to session-selected credentials

**Files:** `sdk/providers/openai/openai.go`, `sdk/providers/anthropic/anthropic.go`, `sdk/providers/gemini/gemini.go`

- [x] Add `WithAPIKey` to each adapter.
- [x] Keep shared adapter instances immutable from the session's perspective.

### Task 5: Document canonical routing semantics

**Files:** `README.md`, this plan

- [x] Document logical provider vs adapter.
- [x] Document session key affinity.
- [x] Document the three requested route examples.

## Verification

Local `go test ./...` passes after the implementation. Provider integration against real credentials is intentionally not part of this change.
