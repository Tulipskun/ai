# Specification Changes

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

## Accepted changes

```text
CHANGE-001

Date: 2026-09-14
Type: revise
Request: Remove stream mode entirely; Harness uses non-streaming only.
Conflict: REQ-009
Previous: Streaming output is not automatically replayed after output has started.
New: The Harness uses non-streaming Generate only; streaming mode is not supported. Added CON-010: Do not reintroduce streaming model-call paths; Generate is the only model call path.
Reason: Streaming adds replay/partial-output complexity without benefit for the tool-loop Harness; single Generate path is simpler and deterministic.
Impact: sdk/types, sdk/router_client, sdk/agent, sdk/client, sdk/providers (openai/anthropic/gemini), cmd/ai/cli, transport/cli display flags, session persistence, all Stream test fakes.
Status: accepted
```

```text
CHANGE-002

Date: 2026-09-14
Type: add
Request: Make sub-agents work like codex/claude-code workers: Main Agent delegates, completion is injected as a prompt naming the sub-agent id, Main reads the final summary, and Main can send follow-up messages into the sub-agent session when work is incomplete.
Conflict: none (no existing sub-agent requirement; extends plan/sub-agent behavior)
Previous: none
New: REQ-016 — The Main Agent delegates work to background sub-agents that run in isolated sessions; when a sub-agent loop ends, the Harness injects a completion prompt naming the sub-agent id so the Main Agent can read its final summary. REQ-017 — The Main Agent can send a follow-up message into a sub-agent session when work is incomplete; the sub-agent continues from its session state and reports completion again.
Reason: Previous fire-and-forget background jobs left results unread and uncontinuable; codex-style delegate/inject-read/follow-up closes the orchestration loop.
Impact: sdk/subagent (Send/follow-up, worker session reuse, completion prompt), sdk/plan_tool (tool + instruction), sdk/loop + cmd/ai/cli (inject prompt), sub-agent tests.
Status: accepted
```
