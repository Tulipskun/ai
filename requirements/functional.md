# Functional Requirements

REQ-001 — The Harness accepts canonical input independently of transport.

REQ-002 — CLI and Discord can share the same Harness/Agent runtime.

REQ-003 — Each session has isolated persistent state and can be restored after process restart.

REQ-004 — The Agent can perform a model → tool call → tool result → model loop until no tool call remains.

REQ-005 — Tool failures are represented as tool results so the Agent can recover without crashing the whole turn.

REQ-006 — Provider-specific request/response formats are isolated behind adapters and converted through the canonical SDK model.

REQ-007 — Provider model discovery uses the live provider catalogue; a successful refresh replaces stale catalogue entries.

REQ-008 — Retries reuse the selected session API key and do not rotate keys implicitly.

REQ-009 — The Harness uses non-streaming Generate only; streaming mode is not supported.

REQ-010 — Browser automation is available through the built-in Go CDP implementation without Playwright or a Node.js worker.

REQ-011 — Runtime configuration is file-based under the configured `~/.local/share/ai` layout and does not require environment-variable configuration.

REQ-012 — Project requirements are stored in the repository and treated as the source of truth for project work.

REQ-013 — Before implementing a new request, the AI checks the repository requirements and records any specification change required by the request.

REQ-014 — When creating a new software project, the AI creates and populates that project's `requirements/` directory before substantial implementation.

REQ-015 — Code changes should preserve clear module responsibility and add or modify functionality in the module that owns the responsibility unless a documented architecture change is required.

REQ-016 — The Main Agent delegates work to background sub-agents that run in isolated sessions; when a sub-agent loop ends, the Harness injects a completion prompt naming the sub-agent id so the Main Agent can read its final summary.

REQ-017 — The Main Agent can send a follow-up message into a sub-agent session when work is incomplete; the sub-agent continues from its session state and reports completion again.
