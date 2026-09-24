# Repository Index

Read this file first. It maps the whole repository in one call so workers
skip 5-10 discovery calls (`list_directory` / `search_files` loops).
For file content, prefer the batch tool `read_files` over repeated
`read_file`: one call returns every file in a single history group,
which the context window (`sdk/context_window.go`) never splits.
Before any code change read this file plus relevant requirements; after any
code change update this file when structure or key files changed and update
requirements plus `requirements/changes.md` when behavior or spec changed.

## Top-level tree

```text
.
├── bin/               empty placeholder (reserved install target)
├── cmd/ai/            daemon entry + agent/registry construction (single gateway)
├── cmd/demo/          throwaway provider-smoke prototype (not shipped)
├── sdk/               provider-neutral Agent runtime, sessions, orchestration
├── tools/             worker execution tools (files, shell, jobs, browser)
├── runtime/           config load, session manager, provider wiring, filestore
├── transport/         mobile transport only (display only, no core logic)
│   └── mobile/        AIxodia WebSocket gateway over a Cloudflare quick tunnel
├── requirements/      source of truth for product behavior (read before code)
├── skills/            contributor procedures (spec management, checklists)
├── AGENTS.md          contributor entry: spec-first rule + module discipline
├── README.md          daemon operation + config layout + AIxodia gateway help
└── index.md           this file (hand-maintained, concise)
```

## Module responsibility → key files

| Responsibility | Key files |
|---|---|
| Turn loop model → tool → model | `sdk/agent.go`, `sdk/loop.go` |
| Loop-control caps (REQ-045) | `sdk/loop_control.go`, `sdk/loop_control_test.go` |
| Planner (Main Agent, senior: read-only context tools) | `sdk/plan_tool.go` |
| Worker delegation contracts, progress/final reports | `sdk/subagent.go`, `sdk/subagent_trace_sink.go` |
| Context budget, newest-group truncation | `sdk/context_window.go` |
| Session persistence (one db per session) | `sdk/session_db.go`, `sdk/session_settings.go` |
| Provider routing, catalogue, retry | `sdk/router_client.go`, `sdk/routing.go`, `sdk/providers/` |
| Worker tool surface | `tools/registry.go` |
| File tools incl. batch read | `tools/files.go` |
| Shell / jobs / fetch / browser / OS input | `tools/command.go`, `tools/jobs.go`, `tools/web_fetch.go`, `tools/browser_tools.go`, `tools/os_input.go` |
| Attachment file store tools | `tools/attachments.go` |
| Outbound file-send intents (worker → transport) | `sdk/outbound_attachments.go` |
| Runtime config + session manager | `runtime/session_manager.go`, `runtime/*.go` |
| Stateless runtime state ↔ Cloudflare D1 | `runtime/d1store/client.go`, `runtime/d1store/sync.go` |
| Mobile gateway (AIxodia, the only transport) + auth gate | `transport/mobile/gateway.go`, `transport/mobile/auth.go`, `transport/mobile/tunnel.go`, `transport/mobile/proxy.go`, `cmd/ai/mobile.go` |

## Key files (what each owns)

- `sdk/agent.go` — provider-neutral control loop; owns request composition,
  tool-result continuation, and per-session planning switch. No transport code.
  Enforces REQ-045 loop-control caps per attempt (fail-fast, never retried).
- `sdk/loop_control.go` — hard loop caps (max tools/turn, max consecutive
  read/edit, max bash output) with the fatal-error classifier. No transport.
- `requirements/loop-control.md` — ordered checklist, per-delegation tool
  budgets, batch-read and stop rules (REQ-045 prompt discipline).
- `requirements/lessons.md` — append-only failure lessons (REQ-045).
- `sdk/subagent.go` — async delegation (`delegate/follow_up/continue` return
  job id at once, `stop` blocks); progress report every X tool calls plus
  complete handoff report (tool names, args, results, error flags).
- `sdk/plan_tool.go` — planner-only tool surface (`plan` + orchestration);
  checklist injected into the planner system prompt every iteration.
- `sdk/context_window.go` — token budget (default 58000); keeps newest
  complete user→response→tools groups unsplit, drops oldest first.
- `sdk/session_db.go` — one SQLite db per session under `data/sessions/`;
  persists settings, turns, and plan state across restarts.
- `tools/registry.go` — worker `Registry`: registers every execution tool,
  resolves per-session workspace root, dispatches `Execute` calls.
- `tools/files.go` — `read_file` (single), `read_files` (batch up to 32,
  per-file + total byte caps, `safePath`/`withinRoot` enforced), `write_file`,
  `edit_file`, `list_directory`, `search_files` (substring, max 200 paths).

## Requirements and docs pointers

- `requirements/README.md` — how to read the spec directory.
- `requirements/product.md` — product purpose and goals.
- `requirements/functional.md` — stable REQ-xxx behavior (check before code).
- `requirements/loop-control.md` — loop checklist + tool budgets (REQ-045).
- `requirements/lessons.md` — failure lessons log (REQ-045).
- `requirements/constraints.md` — non-negotiable limits (CON-xxx).
- `requirements/decisions.md` — accepted architecture decisions.
- `requirements/changes.md` — append-only CHANGE-xxx history.
- `AGENTS.md` — spec-first workflow and module-discipline rule.
- `workflow.md` — turn pipeline and retry/error side paths.
- `docs/cli-mode.md`, `docs/install-layout.md`, `docs/layout-new.md`,
  `docs/session-runtime-settings.md` — transport, install, settings notes.

## Start-here routes

- Fix a turn-loop bug: `sdk/agent.go` + `sdk/loop.go` + `sdk/context_window.go`.
- Change planner/worker behavior: `sdk/plan_tool.go` + `sdk/subagent.go`.
- Add or change a worker tool: `tools/registry.go` + owning `tools/*.go`
  file; keep the change in the owning module (no cross-module refactors).
- Provider / catalogue / retry issue: `sdk/router_client.go` +
  `sdk/routing.go` + `sdk/providers/<adapter>/`.
- Daemon start / gateway / tunnel / D1 hydration: `cmd/ai/main.go`,
  `cmd/ai/mobile.go`, `transport/mobile/`, `runtime/d1store/`.
- Session persist / workspace / settings: `sdk/session_db.go` +
  `sdk/session_settings.go` + `runtime/session_manager.go`.
- Config or state that must come from D1 instead of disk: `runtime/d1store/`
  keys `config:*` and `sessions/<id>` (CON-012).
- Mobile app (AIxodia) gateway: `transport/mobile/` plus `cmd/ai/mobile.go`;
  core orchestration stays untouched. There is no other transport
  (CHANGE-059).
- Stateless runtime state / D1 sync: `runtime/d1store/` only (CON-012 keeps
  `config/*.json` and one SQLite file per session as the local materialization).
- New spec conflict: update `requirements/` + append `requirements/changes.md`
  before implementing (see `AGENTS.md` and `skills/`).

## Batch-read guidance

```json
{"paths": ["index.md", "sdk/agent.go", "tools/files.go"]}
```

- `paths`: up to 32 workspace-relative paths, read in order.
- `max_bytes`: optional per-file content cap (default 4 MiB).
- `max_total_bytes`: optional total content cap (default 4 MiB).
- Result: JSON array of `{path, bytes, truncated, content, error}` —
  per-file errors (missing, directory, escape, over-limit) are entries,
  never a whole-call failure. Oversized single files stay rejected so
  session data never bloats (CON-002, CON-003).
