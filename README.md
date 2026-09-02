# Tulipskun/ai

Prototype canonical Go SDK for an AI Harness.

The SDK keeps conversation history in a provider-neutral format and translates it at the provider boundary. It also separates the **logical provider** used by a session from the **adapter** used to speak a provider API.

## Routing model

A route is explicitly registered as:

```text
logical provider + model -> adapter
```

Examples:

```text
openrouter + gpt-5       -> openai adapter
openrouter + gemini-3.5  -> gemini adapter
opencode   + opus        -> anthropic adapter
```

The model name does not implicitly choose a provider. The logical provider is required for routed requests.

## Session API-key affinity

API keys are scoped to a logical provider and a session can pin one key by index. This means concurrent sessions can use different keys without advancing a shared global cursor.

```go
pool := sdk.NewKeyPool("openrouter-key-1", "openrouter-key-2")

session1 := sdk.NewSession(sdk.SessionConfig{
    ID: "session-1",
    Provider: sdk.ProviderOpenRouter,
    Model: "gpt-5",
    KeyIndex: 0,
}, pool)

session2 := sdk.NewSession(sdk.SessionConfig{
    ID: "session-2",
    Provider: sdk.ProviderOpenRouter,
    Model: "gemini-3.5",
    KeyIndex: 1,
}, pool)
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

The prototype currently contains OpenAI, Anthropic, and Gemini wire adapters. Each adapter can receive a session-selected API key without mutating the shared adapter instance.

OpenRouter is an example of a logical provider whose models can be routed through a compatible adapter; OpenRouter documents OpenAI-compatible API access and model IDs such as `google/gemini-*`. The route registry therefore remains the authority for adapter selection rather than guessing from a model name.

## Prototype

```go
router := sdk.NewRouter()
router.Register(sdk.ModelRoute{
    Provider: sdk.ProviderOpenRouter,
    Model: "gpt-5",
    Adapter: sdk.AdapterOpenAI,
})

client := sdk.NewRouterClient(router)
client.RegisterAdapter(sdk.AdapterOpenAI, openai.New(""))

response, err := client.Generate(ctx, session1, sdk.Request{
    SystemPrompt: "You are an AI coding agent.",
    Messages: []sdk.Turn{{
        Role: sdk.RoleUser,
        Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "hello"}},
    }},
    ThinkingLevel: sdk.ThinkingMedium,
})
```

Provider keys for direct adapter construction are read from `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, and `GEMINI_API_KEY` when not passed explicitly.

This is intentionally a thin prototype. Provider-specific features that cannot be represented safely by the canonical schema are not exposed yet.
