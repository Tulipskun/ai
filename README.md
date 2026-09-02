# Tulipskun/ai

Prototype canonical Go SDK for an AI Harness.

The SDK keeps conversation history in a provider-neutral format and translates it at the provider boundary. It separates the **logical provider** used by a session from the **adapter** used to speak a provider API.

## Harness selection flow

```text
select provider
    ↓
auto-fetch live models
    ↓
replace model catalogue (stale models disappear)
    ↓
select model
    ↓
select API key
    ↓
select thinking level + temperature
    ↓
create session
```

A provider is registered with its base URL, adapter, and provider-scoped key pool. `RouterClient.RefreshModels()` fetches the current catalogue. The latest successful response replaces the old catalogue, so models missing from the provider response are no longer resolvable.

## Retry policy

Retries are deliberately separate from key rotation. The selected session key never changes during a retry.

The default policy is 3 attempts with exponential backoff, capped at 4 seconds. HTTP 429, 500, 502, 503, and 504 are retryable. 400/401/403/404 and context cancellation are not retried. Model discovery uses the same policy.

Streaming is retried only when the failure happens before the stream has emitted an event. Once output has started, the stream is never replayed automatically because replaying it could duplicate user-visible output.

## Session-owned history

A `Session` owns a canonical history and returns defensive copies. Use `GenerateTurn()` or `StreamTurn()` when the Harness should manage a complete user turn transactionally:

```go
response, err := client.GenerateTurn(ctx, session, sdk.Turn{
    Role: sdk.RoleUser,
    Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "hello"}},
}, sdk.Request{})
```

The user turn is committed first, but the model output is committed only after a successful request. If all retries fail, the session is restored to its exact history from before the turn. `StreamTurn()` follows the same rule: streamed output is committed only after `EventDone`; a failed stream restores the previous history.

`Session.RepairHistory()` can also repair externally restored/corrupted history by removing orphan tool results, duplicate tool-call IDs, and incomplete tool calls. It never changes the selected API key.

## Routing and model discovery

Discovered model catalogues are preferred over static routes. The model name does not implicitly choose a logical provider; the provider is part of the session configuration.

```text
openrouter + OpenAI-compatible model -> openai adapter
opencode   + Anthropic-compatible model -> anthropic adapter
Google-native provider + Gemini model -> gemini adapter
```

Example provider setup:

```go
keys := sdk.NewKeyPool("key-1", "key-2")
router := sdk.NewRouter()
router.RegisterProvider(sdk.ProviderConfig{
    ID:      sdk.ProviderOpenRouter,
    BaseURL: "https://openrouter.ai/api/v1",
    Keys:    keys,
    Adapter: sdk.AdapterOpenAI,
})

client := sdk.NewRouterClient(router)
client.RegisterAdapter(sdk.AdapterOpenAI, openai.New(""))

if err := client.RefreshModels(ctx, sdk.ProviderOpenRouter); err != nil {
    panic(err)
}
models := router.Models(sdk.ProviderOpenRouter)
```

## Session settings

API keys are scoped to a logical provider and a session pins one key by index. Thinking level and temperature are session defaults and can be overridden per request.

```go
temperature := 0.7
session := sdk.NewSession(sdk.SessionConfig{
    ID:            "session-1",
    Provider:      sdk.ProviderOpenRouter,
    Model:         models[0].ID,
    KeyIndex:      1,
    ThinkingLevel: sdk.ThinkingHigh,
    Temperature:   &temperature,
}, keys)
```

## Canonical model

- system prompt
- user / model messages
- tool call + ID
- tool result + ID
- stream
- temperature
- thinking level
- max output tokens
- tool definitions
- normalized usage and cache metadata
- explicit logical provider

Provider-specific request/response shapes do not leak into the Harness layer.

## Provider adapters

The prototype contains OpenAI, Anthropic, and Gemini wire adapters. Each adapter supports session-selected API keys and provider-specific base URL injection without mutating the shared adapter instance. Each adapter also implements live model discovery.

Provider keys for direct adapter construction are read from `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, and `GEMINI_API_KEY` when not passed explicitly.

Key rotation is intentionally **not** implemented yet. Retries reuse the same selected key.
