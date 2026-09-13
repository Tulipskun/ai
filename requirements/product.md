# Product Requirements

## Purpose

`ai` is a Go-based AI Harness that provides a provider-neutral Agent runtime with CLI and Discord transports, persistent sessions, tools, browser automation, and provider routing.

## Goals

- Keep the Harness core independent from any single transport.
- Let the Agent reason through tool calls and continue execution from tool results.
- Keep provider-specific wire formats behind adapters and a canonical request/response model.
- Preserve session state so work can continue safely across turns and process restarts.
- Make project behavior explicit and maintainable as requirements evolve.

## Non-goals

- Treating AI memory or chat history as the authoritative project specification.
- Storing project requirements only inside a system prompt.
- Coupling the core Agent to Discord, CLI, or one provider.
