# Tulipskun/ai

Prototype canonical Go SDK for an AI Harness.

The SDK keeps conversation history in a provider-neutral format and translates it at the provider boundary. It separates the **logical provider** used by a session from the **adapter** used to speak a provider API.

## Harness selection flow

The intended Harness flow is:

```text
select provider
    ↓
auto-fetch live models
    ↓
replace the model catalogue (stale models disappear)
    ↓
select model
    ↓
select API key
    ↓
select thinking level + temperature
    ↓
create session
```

A provider is registered with its base URL, adapter, and provider-scoped key pool. `RouterClient.RefreshModels()` then fetches the current model catalogue through the configured adapter. The latest response replaces the old catalogue, so models that no longer exist are no longer resolvable.

```go
router := sdk.NewRouter()
router.RegisterProvider(sdk.ProviderConfig{
    ID:      sdk.ProviderOpenRouter,
    BaseURL: "https://openrouter.ai/api/v1",
    Keys:    sdk.NewKeyPool("key-1", "key-2"),
    Adapter: sdk.AdapterOpenAI,
})

client := sdk.NewRouterClient(router)
client.RegisterAdapter(sdk.AdapterOpenAI, openai.New(""))

if err := client.RefreshModels(ctx, sdk.ProviderOpenRouter); err != nil {
    panic(err)
}

models := router.Models(sdk.ProviderOpenRouter)
```

## Routing model

A static route, when needed, is explicitly registered as:

```text
logical provider + model -> adapter
```

Discovered catalogues are preferred over static routes. Examples of the adapter distinction are:

```text
openrouter + OpenAI-compatible model -> openai adapter
opencode   + Anthropic-compatible model -> anthropic adapter
Google-native provider + Gemini model -> gemini adapter
```

The model name does not implicitly choose a logical provider. The provider is part of the session configuration.

## Session API-key affinity

API keys are scoped to a logical provider and a session can pin one key by index. Concurrent sessions can therefore use different keys without advancing a shared global cursor.

```go
temperature := 0.7
session := sdk.NewSession(sdk.SessionConfig{
    ID:            "session-1",
    Provider:      sdk.ProviderOpenRouter,
    Model:         "openai/gpt-5",
    KeyIndex:      1,
    ThinkingLevel: sdk.ThinkingHigh,
    Temperature:   &temperature,
}, routerProviderKeys)
```

The session's thinking level and temperature become request defaults; an individual request can override them.

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

OpenRouter exposes an OpenAI-compatible API and a live model catalogue, so it can use the OpenAI adapter while retaining `openrouter` as the logical provider. citeturn0search2turn0search5

## Prototype

```go
router := sdk.NewRouter()
router.RegisterProvider(sdk.ProviderConfig{
    ID:      sdk.ProviderOpenRouter,
    BaseURL: "https://openrouter.ai/api/v1",
    Keys:    sdk.NewKeyPool("key-1", "key-2"),
    Adapter: sdk.AdapterOpenAI,
})

client := sdk.NewRouterClient(router)
client.RegisterAdapter(sdk.AdapterOpenAI, openai.New(""))

if err := client.RefreshModels(ctx, sdk.ProviderOpenRouter); err != nil {
    panic(err)
}

models := router.Models(sdk.ProviderOpenRouter)
selected := models[0]
temperature := 0.7
session := sdk.NewSession(sdk.SessionConfig{
    ID:            "session-1",
    Provider:      sdk.ProviderOpenRouter,
    Model:         selected.ID,
    KeyIndex:      0,
    ThinkingLevel: sdk.ThinkingMedium,
    Temperature:   &temperature,
}, routerProviderKeys)

response, err := client.Generate(ctx, session, sdk.Request{
    SystemPrompt: "You are an AI coding agent.",
    Messages: []sdk.Turn{{
        Role: sdk.RoleUser,
        Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "hello"}},
    }},
})
```

Provider keys for direct adapter construction are read from `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, and `GEMINI_API_KEY` when not passed explicitly.

This is intentionally a thin prototype. Automatic key rotation/retry policy and provider-specific capability negotiation beyond the discovered model metadata are separate follow-up work.
