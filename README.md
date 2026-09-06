# Tulipskun/ai

Prototype canonical Go SDK for an AI Harness.

The SDK keeps conversation history in a provider-neutral format and translates it at the provider boundary. It separates the **logical provider** used by a session from the **adapter** used to speak a provider API.

## CLI

The application entry point is `cmd/ai`. Build it as the `ai` command and start the Harness with:

```bash
go build -o ai ./cmd/ai
./ai start
```

For development, the same command can be run with:

```bash
go run ./cmd/ai start
```

The command accepts `start` explicitly. Running `ai` with no subcommand remains equivalent to `ai start` for backwards compatibility. `ai --help` shows the available commands.

Use `ai update` as the single update command. It pulls the latest code, builds a replacement binary before stopping the current service, then restarts the supervisor with the new binary. The supervisor script remains an implementation detail.

```bash
ai update
```

To use the terminal as an input transport, enable it with `AI_CLI_ENABLED=true`. Provider and model selection can be configured at runtime instead of requiring them before startup.

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

Providers may also be added from Discord with `/provider`, or from the CLI with `/provider add <name> <adapter> <url> <api-key>`.

## Supervisor

The supervisor keeps the built AI process running. Manual supervisor control is available for service administration:

```bash
bash scripts/supervisor.sh run
bash scripts/supervisor.sh stop
```

Normal software updates should use `ai update` rather than calling the supervisor update mode directly.

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

The default policy is 3 attempts with exponential backoff, capped at 4 seconds. HTTP 404 is the only explicitly non-retryable HTTP status; provider-specific 400 responses are retryable. Context cancellation and deadline errors are not retried. Model discovery uses the same policy.

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

## Web fetch and browser automation

`web_fetch` is the lightweight path for static HTTP/HTTPS pages, documentation, and APIs. Browser automation uses Playwright with Chromium in a separate Node.js worker for interactive sites.

## Input and display architecture

The Harness core is transport-independent. Input sources convert external events into canonical `Input` values, while displays consume canonical `Output` values. Discord and CLI can be enabled at the same time and share the same Harness/Agent runtime.

## Discord runtime

The concrete transport is `transport/discord`. `DISCORD_OWNER_ID` is the single Discord user ID authorized to use bot interactions. Model and provider settings can be managed through Discord slash commands.

## Routing and model discovery

Discovered model catalogues are preferred over static routes. The model name does not implicitly choose a logical provider; the provider is part of the session configuration.

## Provider adapters

The prototype contains OpenAI, Anthropic, and Gemini wire adapters. Each adapter supports session-selected API keys and provider-specific base URL injection without mutating the shared adapter instance. Each adapter also implements live model discovery.

Key rotation is intentionally **not** implemented yet. Retries reuse the same selected key.
