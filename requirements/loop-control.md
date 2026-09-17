# Loop Control Checklist (REQ-045)

Dual control for worker loops: hard system caps fail a runaway turn
automatically (`sdk/loop_control.go`), and this checklist keeps planners
and workers disciplined so the caps are never hit in normal work.

## Ordered checklist per task

Every delegated task follows this order. No step is skipped, no step is
repeated once its outcome is proven.

1. Read `index.md` first in one call.
2. Batch needed files with `read_files` in one call — never repeated
   `read_file` or discovery loops for files already known.
3. Re-check `index.md` plus `requirements/` before and after code changes.
4. Implement the smallest change that satisfies the step.
5. Validate with the minimal sufficient check: one command that proves the
   outcome (or a single combined shell line for related checks).
6. Report file paths changed, the validation command and its outcome, and
   anything left unresolved.

## Tool budget declaration per delegation

Every `delegate_to_subagent` / `follow_up_subagent` / `continue_subagent`
task states its budget explicitly, for example:

```text
Tool budget: max 6 reads, max 2 edits, max 3 bash calls.
Batch reads via read_files. If the budget is exceeded, stop as failed
and write a lesson to requirements/lessons.md instead of looping.
```

Guidance: single-digit totals per step. A step that needs more than ~10
tool calls is a step that must be split.

## Batch reads

- `read_files` (up to 32 paths, one call) is the default for multi-file
  reads, including the `index.md`-first + batch pattern.
- `read_file` is for a genuinely single file only.
- Never `list_directory` / `search_files` in a loop to rediscover what a
  previous call already returned.

## Stop condition

Budget exceeded = fail, not retry:

1. Stop calling tools immediately.
2. Return the failure with the counts (reads/edits/bash used).
3. Append a lesson to `requirements/lessons.md` (what looped, why, what
   budget or batching would have prevented it).
4. Never retry a loop-control failure in the same shape — the harness
   treats budget errors as fatal and will not retry them automatically.

## System caps (reference)

Enforced in `sdk/loop_control.go`; quoted here so prompts and code agree:

- Max 30 executed tool calls per turn attempt — then auto-fail.
- Max 8 consecutive read/edit-probe calls (`read_file`, `read_files`,
  `list_directory`, `search_files`, `edit_file`) without an intervening
  progress tool — then auto-fail as a stall.
- Max 1 MiB per `bash` tool result payload — then auto-fail.
- Budget failures are never retried at the turn level.

Staying under budget is part of verified success (planner prompt,
worker prompt).
