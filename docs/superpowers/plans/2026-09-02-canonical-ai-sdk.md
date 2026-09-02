# Canonical AI SDK Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a thin canonical Go SDK that can translate one conversation representation to OpenAI, Anthropic, or Gemini.

**Architecture:** `sdk` owns canonical types and provider-neutral interfaces. Each provider adapter owns request/response translation and SSE parsing. No provider-native types cross the `sdk` boundary.

**Tech Stack:** Go 1.23+, standard library `net/http`, JSON, SSE; no external dependencies in the prototype.

**Spec:** `docs/superpowers/specs/2026-09-02-canonical-ai-sdk-design.md`

## Global Constraints

- Keep the canonical API small.
- Preserve tool call/result IDs across provider translation.
- Normalize cache-read/write token usage when available.
- Do not expose provider-native request types from `sdk`.

### Task 1: Canonical types and provider interface

- [x] Define canonical roles, content, tool calls/results, request/response, usage/cache, and stream events.
- [x] Define `Provider` and `Client` wrappers.
- [x] Test canonical tool-call/result ID preservation.

### Task 2: Provider adapters

- [x] Implement HTTP JSON translation for OpenAI, Anthropic, and Gemini.
- [x] Implement SSE stream translation for text/tool-call/done/error events.
- [x] Normalize usage and provider cache-read/write counters.
- [x] Test provider-specific role/tool translation.

### Task 3: Prototype documentation and demo

- [x] Document the canonical schema and provider selection.
- [x] Add a compile-only demo that constructs all three clients.

### Task 4: Verification

- [x] Run `gofmt`.
- [x] Run `go test ./...` successfully in the local prototype checkout.
