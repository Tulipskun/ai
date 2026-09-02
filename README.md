# Tulipskun/ai

Prototype canonical Go SDK for an AI Harness.

The SDK keeps conversation history in a provider-neutral format and translates it at the provider boundary. The prototype supports OpenAI Responses API, Anthropic Messages API, and Gemini GenerateContent API, including tool calls/results, streaming events, temperature, thinking level, token usage, and provider cache-read/write usage where available.

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

Provider-specific request/response shapes do not leak into the Harness layer.

## Prototype

```go
client := sdk.NewClient(openai.New(""))
response, err := client.Generate(ctx, sdk.Request{
    Model: "gpt-5.1",
    SystemPrompt: "You are an AI coding agent.",
    Messages: []sdk.Turn{{
        Role: sdk.RoleUser,
        Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "hello"}},
    }},
    ThinkingLevel: sdk.ThinkingMedium,
})
```

Provider keys are read from `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, and `GEMINI_API_KEY` when not passed explicitly.

This is intentionally a thin prototype. Provider-specific features that cannot be represented safely by the canonical schema are not exposed yet.
