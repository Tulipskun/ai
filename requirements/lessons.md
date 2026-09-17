# Failure Lessons (REQ-045)

Append-only log of loop failures and the discipline that prevents them.
Each entry: what looped, why, and the budget/batching rule that stops it
next time. Workers append here instead of retrying a blown budget.

## Format

```text
LESSON-XXX

Date: YYYY-MM-DD
Symptom: <tool counts, visible loop>
Cause: <why it looped>
Fix: <budget/batching/cap rule applied>
```

---

LESSON-001

Date: 2026-09-17
Symptom: Slide-window panel work burned 47 worker tool calls — repeated
`sed` edit loops plus drifting test expectations re-run after every edit.
Cause: No per-delegation tool budget, edits applied one `sed` invocation
at a time instead of batched reads + single deliberate edits, and
validation re-ran overlapping checks after the outcome was already proven.
Fix: REQ-045 dual control — max 30 tool calls per turn and max 8
consecutive read/edit probes without progress fail the turn fast;
planner declares a tool budget per delegation (single-digit reads/edits/
bash), worker batches reads via `read_files`, validates once with the
minimal sufficient check, and writes a lesson instead of looping.
