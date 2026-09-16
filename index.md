# Repository Index

Read this file first. It maps the whole repository in one call so workers
skip 5-10 discovery calls (`list_directory` / `search_files` loops).
For file content, prefer the batch tool `read_files` over repeated
`read_file`: one call returns every file in a single history group,
which the context window (`sdk/context_window.go`) never splits.

## Top-level tree

```text
.
├── cmd/ai/            CLI entry, daemon wiring, agent/registry construction
├── sdk/               provider-neutral Agent runtime, sessions, orchestration
├── tools/             worker execution tools (files, shell, jobs, browser)
├── runtime/           config load, session manager, provider wiring, filestore
├── transport/         CLI + Discord transports (display only, no core logic)
│   ├── cli/
│   └── discord/
├── requirements/      source of truth for product behavior (read before code)
├── docs/              install, layout, session settings, CLI mode notes
├── skills/            contributor procedures (spec management, checklists)
├── scripts/           install / supervisor helpers
├── AGENTS.md          contributor entry: spec-first rule + module discipline
├── workflow.md        turn pipeline diagram (Harness → Agent → Router → tool)
├── README.md          install, config layout, provider/browser/attachment help
└── index.md           this file (hand-maintained, concise)
```

## Module responsibility → key files

| Responsibility | Key files |
|---|---|
| Turn loop model → tool → model | `sdk/agent.go`, `sdk/loop.go` |
| Planner (Main Agent, no exec tools) | `sdk/plan_tool.go` |
| Worker delegation, progress/final reports | `sdk/subagent.go`, `sdk/subagent_trace_sink.go` |
| Context budget, newest-group truncation | `sdk/context_window.go` |
| Session persistence (one db per session) | `sdk/session_db.go`, `sdk/session_settings.go` |
| Provider routing, catalogue, retry | `sdk/router_client.go`, `sdk/routing.go`, `sdk/providers/` |
| Worker tool surface | `tools/registry.go` |
| File tools incl. batch read | `tools/files.go` |
| Shell / jobs / fetch / browser | `tools/command.go`, `tools/jobs.go`, `tools/web_fetch.go`, `tools/browser_tools.go` |
| Attachment file store tools | `tools/attachments.go` |
| Runtime config + session manager | `runtime/session_manager.go`, `runtime/*.go` |
| Discord transport + trace display | `transport/discord/gateway.go`, `transport/discord/actor_trace_display.go` |

## Key files (what each owns)

- `sdk/agent.go` — provider-neutral control loop; owns request composition,
  tool-result continuation, and per-session planning switch. No transport code.
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
- Session persist / workspace / settings: `sdk/session_db.go` +
  `sdk/session_settings.go` + `runtime/session_manager.go`.
- Discord display / trace / command: `transport/discord/` only; core
  orchestration and canonical contracts stay untouched.
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
