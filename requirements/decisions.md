# Architecture Decisions

DEC-001 — Requirements live in the project repository rather than in AI memory.

Reason: specifications must remain available to every future agent, contributor, branch, and clone of the project.

DEC-002 — Project requirements are separated into product goals, functional requirements, constraints, decisions, and change history.

Reason: separate stable intent from implementation constraints and historical changes so agents can reason about impact without rewriting the whole specification.

DEC-003 — A conflicting user request is handled as a requirement change before implementation.

Reason: silently implementing conflicting behavior creates undocumented drift between requirements and code.

DEC-004 — New software projects created by the AI receive a `requirements/` directory in the project repository before substantial implementation.

Reason: the project repository must carry its own specification from its first implementation onward.
