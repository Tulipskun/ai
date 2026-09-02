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

## Runtime provider configuration

Runtime provider settings live in `.config/provider.json`. The file is intentionally ignored by Git because it contains API keys. A safe template is provided at `.config/provider.example.json`.

```json
{
  "providers": [
    {
      "name": "openrouter",
      "http_endpoint": "https://openrouter.ai/api/v1",
      "api_keys": ["key-1", "key-2"]
    }
  ]
}
```

The runtime loader is:

```go
rt, err := runtime.Load(".config/provider.json")
if err != nil { panic(err) }
if err := rt.RefreshModels(ctx); err != nil { panic(err) }
```

The runtime config contains only provider name, HTTP endpoint, and API key array. The adapter is inferred from the provider name: `openai`/`openrouter` use the OpenAI-compatible adapter, `anthropic`/`opencode` use the Anthropic adapter, and `gemini`/`google` use the Gemini adapter.

## Retry policy

Retries are deliberately separate from key rotation. The selected session key never changes during a retry.

The default policy is 3 attempts with exponential backoff, capped at 4 seconds. HTTP 429, 500, 502, 503, and 504 are retryable. 400/401/403/404 and context cancellation are not retried. Model discovery uses the same policy.

Streaming is retried only when the failure happens before the stream has emitted an event. Once output has started, the stream is never replayed automatically because replaying it could duplicate user-visible output.

## Persistent SQLite sessions

Sessions can be backed by a `.db` file instead of keeping history only in memory:

```go
session, err := sdk.OpenSession("sessions/user-123.db", config, keys)
if err != nil { panic(err) }
defer session.Close()
```

`OpenSession()` creates the database if needed and reloads the existing canonical history for `config.ID`. `Session.Append()` and `Session.ReplaceHistory()` persist the history, so Agent rollback also persists the rolled-back state. The database uses SQLite WAL mode for concurrent readers and transactional updates.

The database records more than the reconstructed conversation history. It keeps:

- `sessions`: session configuration and timestamps.
- `turns`: every canonical user/model/tool-call/tool-result turn.
- `requests`: every model request attempt, including system prompt, full message history, tools, provider, model, temperature, thinking level, max output tokens, and streaming flag.
- `responses`: every corresponding model response or provider error, including content, tool calls, finish reason, usage, cache metadata, and error text.

Therefore provider retries are visible individually in the database rather than being collapsed into one successful request. The request/response records are append-only; session `turns` represent the current durable conversation state.

The SQLite driver is `modernc.org/sqlite`, a CGo-free pure-Go SQLite implementation.

## Session-owned history

A `Session` owns a canonical history and returns defensive copies. Use `GenerateTurn()` or `StreamTurn()` when the Harness should manage a complete user turn transactionally.

The user turn is committed first, but the model output is committed only after a successful request. If all retries fail, the session is restored to its exact history from before the turn. `StreamTurn()` follows the same rule: streamed output is committed only after `EventDone`; a failed stream restores the previous history.

`Session.RepairHistory()` can repair externally restored/corrupted history by removing orphan tool results, duplicate tool-call IDs, and incomplete tool calls. It never changes the selected API key.

## Agent loop

`Agent` is the provider-neutral control loop above `RouterClient`. It repeatedly performs model → tool calls → tool results → model until the model returns no tool calls or the hard iteration limit is reached. Tool failures are returned to the model as `tool_result` entries with `IsError=true`, allowing the model to recover instead of crashing the whole turn.

```go
registry, err := tools.NewRegistry("/workspace/project")
if err != nil { panic(err) }
agent := &sdk.Agent{Client: client, Tools: registry, MaxIterations: 20}
response, err := agent.RunTurn(ctx, session, userTurn, sdk.Request{})
```

`HarnessLoop` can use `Agent` directly. If `Agent` is nil, the existing one-request `RouterClient.GenerateTurn()` path remains available for compatibility.

The coding registry exposes:

- `read_file`
- `write_file`
- `edit_file`
- `list_directory`
- `search_files`
- `run_command`
- `run_job`
- `check_job`
- `close_job`

File operations are restricted to the configured workspace root. `edit_file` requires exactly one match for `old_text`, preventing an ambiguous edit from silently modifying multiple locations.

`run_command` is synchronous. For long-running work, `run_job` starts the command asynchronously and returns a job ID. The agent can call `check_job` later to inspect state/output, or `close_job` to terminate a running job. Job state is process-local and in-memory in this first implementation, and captured output is bounded.

The agent intentionally has no `time` tool. Timestamp information can be attached to each request by the host/application layer.

## Input and display architecture

The Harness core is transport-independent. Input sources convert external events into canonical `Input` values, while displays consume canonical `Output` values.

```text
Discord / Web / Console / ...
            ↓
       InputSource
            ↓
       HarnessLoop
            ↓
         Session
            ↓
          AI API
            ↓
         Session
            ↓
          Output
       ↙     ↓      ↘
  Discord   Web    Console
  Display  Display  Display
```

The turn ordering is strict:

```text
input
  ↓
save to session
  ↓
request AI
  ↓
response
  ↓
save response to session
  ↓
display (async)
```

Display is a best-effort side effect. `HarnessLoop` never waits for a display implementation to finish. Display errors and panics are isolated from the processing loop, and a display timeout bounds the lifetime of its goroutine.

Discord is currently represented by `DiscordInputSource`, `DiscordToInput`, and `DiscordDisplay`. The actual Discord Gateway client stays outside the Harness core and only needs to translate its events into `DiscordInputMessage` and implement `DiscordSender`.

## Routing and model discovery

Discovered model catalogues are preferred over static routes. The model name does not implicitly choose a logical provider; the provider is part of the session configuration.

```text
openrouter + OpenAI-compatible model -> openai adapter
opencode   + Anthropic-compatible model -> anthropic adapter
Google-native provider + Gemini model -> gemini adapter
```

## Session settings

API keys are scoped to a logical provider and a session pins one key by index. Thinking level and temperature are session defaults and can be overridden per request.

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
