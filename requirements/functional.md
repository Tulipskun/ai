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

REQ-016 — With planning enabled, the Main Agent receives only planning and configured sub-agent orchestration tools and rejects direct execution calls, including after a plan is recorded. Main-agent default instructions delegate project investigation and execution rather than directing the main agent to use worker tools.

REQ-017 — With `DisablePlanning: true`, an execution/worker agent retains its supplied system prompt and execution tool definitions in both normal and streaming turns, including tool-result continuations; the Harness does not inject main-agent restrictions or planning/delegation tools. Default sub-agent instructions limit work to the assigned scope, prohibit further delegation and end-user communication, and require reporting findings/results to the planner.

REQ-018 — Role prompt composition preserves repository requirements and custom context rather than truncating sections or removing arbitrary lines by tool-name matching. Main-agent role boundaries take precedence over conflicting direct-execution instructions in supplied context; built-in CLI and SDK defaults must not contradict their assigned role.

REQ-019 — A worker loop ending only produces a result for main-agent review; it never accepts or advances a plan. The main agent reads the terminal result/history, verifies success, and explicitly accepts the captured step through orchestration. Failed, stopped, or incomplete results remain retryable through follow-up in the same worker session. Planner guidance waits for lifecycle completion events rather than repeatedly polling; explicit status requests remain available.

REQ-020 — Delegation atomically reserves at most one running job per parent session across investigation, planned work, and follow-up. Jobs capture plan revision and step identity; completion, retry, and acceptance validate transitions and cannot mutate a replacement plan. History, status, stop, follow-up, and acceptance enforce parent ownership. A completed plan permits investigation for a new task without clearing or bypassing active work.

REQ-021 — Lifecycle continuations preserve the initiating input's canonical source, session routing identity, and original metadata (including Discord channel_id), independently of session-ID spelling. Continuation failures use the existing turn-error reporting path. Existing cancellation behavior and session persistence boundaries remain unchanged; orchestration reservations/revisions are process-local.

REQ-022 — Discord renders canonical response text as readable Markdown, not serialized SDK output. Progress is concise and excludes raw tool arguments/results, delegated task text, reasoning text and internal planning/review chatter; safe operation labels, failure indicators, retry timing, usage and elapsed time remain useful. Transport rendering stays in the Discord module.

REQ-023 — Discord final and streamed response text is paginated losslessly at Unicode code-point boundaries within message/embed limits (counting supplementary characters conservatively as UTF-16 units). Boundary whitespace is retained; fenced code blocks are closed/reopened for page display without removing source content. Streaming updates existing pages and terminal response traces do not replay already streamed content. Progress may remain a bounded summary; response text must not be truncated.

REQ-024 — Discord text/progress transitions propagate flush failures and retain the unsent buffered state for retry instead of resetting it. Successful page sends are tracked so retries do not duplicate them. Success/error terminal paths stop live footer updates, attempt buffered flushing and retry-status cleanup even if another operation fails, and surface display errors. Existing trace throttling/cooldown remains; reset/close cancels pending timers.
