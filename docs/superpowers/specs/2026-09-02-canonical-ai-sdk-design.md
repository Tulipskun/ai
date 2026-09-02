# Canonical AI SDK Design

## Goal

Provide a provider-neutral Go interface for the AI Harness so a conversation can move between OpenAI, Anthropic, and Gemini without storing history in any provider's native format.

## Canonical request

The prototype exposes a system prompt, user/model turns, tool calls and results with IDs, tool definitions, model, temperature, thinking level, max output tokens, and streaming.

## Canonical response

Responses contain normalized text content, tool calls, finish reason, model/provider, token usage, and cache metadata. `CacheReadTokens` and `CacheWriteTokens` represent provider-side prompt caching where the provider reports it; `CacheInfo` is reserved for Harness/application cache state.

## Translation boundary

Each provider adapter translates the canonical request to its native API and translates the response/stream back. Provider-specific schemas must not leak into `sdk` types.

## Prototype transport

The first implementation uses standard-library HTTP/SSE instead of importing three provider SDKs. This keeps the canonical layer small and makes the translation behavior explicit. Official provider SDKs can replace transport internals later without changing the canonical interface.

## Non-goals

- full feature parity with every provider
- embeddings, image generation, audio generation, batch APIs
- automatic model routing/fallback
- persistent conversation storage
- provider-specific escape-hatch parameters
