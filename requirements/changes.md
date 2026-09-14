# Specification Changes

This file is append-only in intent: each accepted specification change should record the previous requirement, the new requirement, reason, affected areas, and validation needed.

## Change format

```text
CHANGE-XXX

Date: YYYY-MM-DD
Type: add | revise | remove
Request: <user request>
Conflict: <requirement IDs or none>
Previous: <previous requirement text when revising/removing>
New: <new requirement text>
Reason: <why the specification changes>
Impact: <architecture/modules/tests/docs affected>
Status: proposed | accepted | rejected
```

CHANGE-001

Date: 2026-09-14
Type: add
Request: Correct main/worker prompt and tool separation in normal and streaming turns without expanding orchestration lifecycle behavior.
Conflict: none (clarifies REQ-004, REQ-012 and REQ-015)
Previous: Role separation and preservation of prompt context were not explicitly specified; main prompt sanitation could discard repository requirements and unrelated context.
New: REQ-016 restricts main-agent tools and direct execution; REQ-017 preserves worker prompts/execution tools without injected planner tools in both paths; REQ-018 preserves repository/custom context and resolves role conflicts without heuristic deletion.
Reason: Workers with execution tools must not be instructed to act as tool-less planners, and main defaults must not instruct direct execution or discard the repository source of truth.
Impact: SDK agent request composition and planning prompt composition; SDK worker and CLI main prompt defaults; focused SDK/CLI regressions, including real registry tool definitions. Existing executor filtering remains authoritative. No architecture, configuration, persistence, lifecycle or transport redesign.
Validation: Normal/streaming worker prompt and execution continuations; actual delegated worker tools and context; main tool allowlist and execution rejection before/after planning; CLI defaults/context tests; go test ./sdk ./runtime ./cmd/ai ./transport/discord -timeout 2m; git diff --check.
Status: accepted

CHANGE-002

Date: 2026-09-14
Type: add
Request: Reliable sequential orchestration with explicit main acceptance, same-session retries, captured plan identity, parent isolation, atomic reservations, and transport-independent lifecycle routing.
Conflict: none (clarifies REQ-001, REQ-003, REQ-016; intentionally replaces implicit worker-success advancement)
Previous: Worker loop completion implicitly advanced the current plan; job ownership/reservations and lifecycle transport metadata were not explicitly specified.
New: REQ-019 gates advancement on reviewed, explicitly verified acceptance and event-driven waiting; REQ-020 binds operations to parent/revision/step and prevents overlapping jobs; REQ-021 preserves canonical input routing and reports continuation failures.
Reason: A textual blocked report is not verified success, stale work must not change replacement plans, and mapped session IDs must not lose transport routing.
Impact: SDK session plan transitions, sub-agent manager/tools/prompts, Harness lifecycle continuation routing, focused SDK/runtime/CLI/Discord tests. No transport presentation changes, configuration changes, or persistence redesign.
Validation: Acceptance gating (including blocked text), retry/history continuity, stale completion, concurrent overlap, cross-parent denial, completed-plan investigation, metadata/source routing and continuation errors; go test ./sdk ./runtime ./cmd/ai ./transport/discord -timeout 2m and focused race tests.
Status: accepted

CHANGE-003

Date: 2026-09-14
Type: add
Request: Polish Discord final/progress rendering, lossless Unicode/fenced-code pagination, and reliable flush/error cleanup without live Discord access.
Conflict: none (clarifies REQ-001, REQ-002, REQ-009, REQ-015 and REQ-021; replaces raw output JSON and lossy text presentation)
Previous: Discord presentation, argument privacy, pagination and flush failure behavior were not explicitly specified.
New: REQ-022 defines readable responses and non-sensitive concise progress; REQ-023 requires lossless final/streamed pagination without terminal replay; REQ-024 requires retained buffers, surfaced failures, retry-safe page tracking and terminal cleanup.
Reason: End users need readable answers rather than SDK envelopes, private execution parameters must not leak via progress, and long multilingual/code responses and failed sends must not silently lose content.
Impact: Discord adapter/gateway, a transport-local paginator, and mock-based rendering/routing tests. No core orchestration, persistence, configuration or transport contract redesign.
Validation: Thai/emoji and whitespace round trips, long fenced code, streamed page updates/terminal non-duplication, secret-argument suppression, send/edit transition failures and retry, terminal cleanup, existing throttling/footer tests; go test ./transport/discord -timeout 2m; go test ./sdk ./runtime ./cmd/ai ./transport/discord -timeout 2m; git diff --check. No live Discord messages or credentials.
Status: accepted
