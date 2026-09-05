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
- `web_fetch`
- `browser_open`
- `browser_close`
- `browser_navigate`
- `browser_snapshot`
- `browser_click`
- `browser_fill`
- `browser_press`
- `browser_select`
- `browser_scroll`
- `browser_get_text`
- `browser_screenshot`

File operations are restricted to the configured workspace root, including symlink-aware path validation. `edit_file` requires exactly one match for `old_text`, preventing an ambiguous edit from silently modifying multiple locations.

`run_command` is synchronous. For long-running work, `run_job` starts the command asynchronously and returns a job ID. The agent can call `check_job` later to inspect state/output, or `close_job` to terminate a running job. Job metadata is persisted under `.ai/jobs/jobs.json`; jobs that were running when the process stopped are restored as failed because their OS process cannot be safely resumed. Captured output is bounded.

The agent intentionally has no `time` tool. Timestamp information can be attached to each request by the host/application layer.

## Web fetch and browser automation

`web_fetch` is the lightweight path for static HTTP/HTTPS pages, documentation, and APIs. It extracts text and a page title instead of returning raw HTML by default.

Browser automation uses Playwright with Chromium in a separate Node.js worker. Use browser tools when the site requires JavaScript execution, interaction, login, form submission, or other page actions.

```text
static page / API / documentation
        ↓
    web_fetch

interactive / dynamic website
        ↓
   browser_open
        ↓
 browser_navigate
        ↓
 browser_snapshot
        ↓
 click / fill / press / select / scroll
```

Each AI session gets an isolated browser context. Multiple tabs can exist inside that context. Browser snapshot references are scoped to the snapshot and must be refreshed after page changes.

Browser setup is opt-in for existing deployments. From the repository root, run as root:

```bash
bash scripts/setup-browser.sh
```

The setup script verifies Node.js, installs the locked Node dependencies, installs Chromium with its required system dependencies, and performs a launch check. It does not use `sudo`.

Enable the browser worker with:

```text
AI_BROWSER_ENABLED=true
AI_BROWSER_HEADLESS=true
```

See `.config/transport.example.env` for all browser settings.

Web access has SSRF protection by default. Loopback, private, link-local, IPv6 local/private, and metadata-style destinations are blocked consistently by the Go and browser-worker policies. Set `AI_BROWSER_ALLOW_PRIVATE=true` only when access to private network services is intentionally required.

Webpage content is untrusted external data. It must not be treated as system or tool instructions.

V1 deliberately does not expose arbitrary JavaScript/eval, raw CDP, arbitrary filesystem access, cookie export/import, `file://` navigation, or arbitrary downloads/uploads.

## Input and display architecture

The Harness core is transport-independent. Input sources convert external events into canonical `Input` values, while displays consume canonical `Output` values.

```text
Discord / Telegram / Web / Console / ...
                 ↓
            InputSource
                 ↓
            HarnessLoop
                 ↓
               Session
                 ↓
                 AI
                 ↓
               Output
                 ↓
             OutputRouter
          ↙       ↓       ↘
      Discord   Telegram   Web
```

Transport adapters live outside `sdk`. The SDK knows only canonical `Input`, `Output`, `InputSource`, `Display`, and the optional source-aware `RoutedDisplay` contract. This means adding Telegram or a WebSocket/HTTP application does not require changing the Harness.

A future transport only needs to translate its native events at this boundary; it must not introduce transport-specific types or dependencies into `sdk`.

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

`HarnessLoop` serializes turns for the same `SessionID` while allowing different sessions to run independently. Display is a best-effort side effect; the loop does not wait for the transport send to finish, and display panics/errors are isolated.

## Discord runtime

The first concrete transport is `transport/discord`. It uses DiscordGo as the Gateway/REST client while keeping Discord-specific types outside `sdk`. DiscordGo v0.29.0 supports the Gateway and is compatible with the project's Go 1.25 toolchain.

The runnable entry point is:

```text
cmd/ai
```

Required environment variables:

```text
DISCORD_BOT_TOKEN=...
DISCORD_OWNER_ID=...
AI_MODEL=...
```

`DISCORD_OWNER_ID` is the single Discord user ID authorized to use the bot's interactions. `AI_PROVIDER` is required when `.config/provider.json` contains more than one provider. If exactly one provider is configured, it can be omitted. Optional variables include `AI_SYSTEM_PROMPT`, `AI_THINKING_LEVEL`, `AI_TEMPERATURE`, `AI_MAX_OUTPUT_TOKENS`, `AI_SESSION_DB`, and `AI_PROVIDER_CONFIG`.

The example is `.config/transport.example.env`. Runtime session databases are stored under `.data/` by default and are ignored by Git.

Discord message sessions default to `discord:channel:<channel_id>`, so the same Discord conversation reuses the same durable Session. The transport ignores bot-authored messages.

Discord message handling requests guild messages, direct messages, and message content. `MESSAGE_CONTENT` is a privileged Discord intent; it must be enabled in the application's Bot settings, and verified/verification-eligible apps may also need approval.

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
- provider-native reasoning state

Provider-specific request/response shapes do not leak into the Harness layer.

## Provider adapters

The prototype contains OpenAI, Anthropic, and Gemini wire adapters. Each adapter supports session-selected API keys and provider-specific base URL injection without mutating the shared adapter instance. Each adapter also implements live model discovery.

Provider keys for direct adapter construction are read from `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, and `GEMINI_API_KEY` when not passed explicitly.

Key rotation is intentionally **not** implemented yet. Retries reuse the same selected key.

Streaming preserves provider-native reasoning/tool state through the canonical `EventReasoning` and `EventToolCall` events. Anthropic accumulates partial tool JSON per content block; OpenAI reasoning-summary deltas and Gemini thought parts are surfaced as reasoning events.
