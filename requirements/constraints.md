# Constraints

CON-001 — Runtime configuration is stored in `config/*.json`; do not introduce `.env`-based configuration.

CON-002 — Session databases remain isolated: one session maps to one database under `data/sessions/`.

CON-003 — Do not add raw provider request/response storage that unnecessarily grows session data; persist the structured state needed for continuation, replay, and accounting.

CON-004 — The Agent remains provider-neutral and transport-independent.

CON-005 — Provider adapters must not mutate shared adapter configuration when applying session-specific settings.

CON-006 — Browser automation remains a built-in Go CDP implementation; do not add Playwright or a Node.js browser worker.

CON-007 — Requirement files in the repository are authoritative for that project. Chat memory is context, not a substitute for repository specification.

CON-008 — Do not silently weaken or remove an existing requirement to make a new implementation easier. Record intentional changes as specification changes.

CON-009 — Avoid unrelated refactors while implementing a requirement change.

CON-010 — Do not reintroduce streaming model-call paths; Generate is the only model call path.
