# Tulipskun/ai

Prototype canonical Go SDK for an AI Harness.

## Install

Install the `ai` command on Linux or macOS with one command. The installer downloads only the prebuilt binary committed under `bin/`; it does not require Git or Go. Runtime state is kept under `~/.local/share/ai` by default.

```bash
curl -fsSL https://raw.githubusercontent.com/Tulipskun/ai/main/scripts/install.sh | bash
```

Then start it with:

```bash
ai start
```

GitHub Actions builds Linux amd64/arm64 and macOS amd64/arm64 binaries and commits them to `bin/` after source changes. Updates download the matching binary from the repository:

```bash
ai update
```

The installer and updater verify the downloaded binary against `bin/checksums.txt`. `AI_VERSION` can be set to a branch, tag, or commit ref when a pinned repository version is required.

To completely remove the installation, including the daemon, runtime state, sessions, provider configuration, logs, and history, run:

```bash
ai uninstall
```

`ai uninstall` removes the binary path being invoked and the default runtime directory `~/.local/share/ai` (or `AI_DATA_DIR` when set). It also cleans up the legacy executable stored inside the runtime directory.

If `~/.local/bin` is not already on `PATH`, add it to the shell profile:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

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

Running `ai` with no subcommand remains equivalent to `ai start` for backwards compatibility. `ai --help` shows the available commands.

Use `ai update` as the single update command. Use `ai uninstall` to remove the installation. The supervisor script remains an implementation detail for source-based development and older installations.

## Configuration

Runtime configuration is file-based. The application does not create or load a `.env` file.

```text
~/.local/share/ai/
├── .config/
│   ├── provider.json
│   ├── input.json
│   └── browser.json
├── .data/
│   ├── sessions.db
│   └── cli.history
└── .ai/
    ├── ai.pid
    └── ai.log
```

The configuration files are intentionally separated by responsibility:

- `.config/provider.json` contains provider endpoints, adapters, and API keys.
- `.config/input.json` contains input transport settings such as Discord credentials/authorization and whether the CLI input is enabled.
- `.config/browser.json` contains browser automation settings.

Examples are provided as `.config/provider.example.json`, `.config/input.example.json`, and `.config/browser.example.json`. Copy an example into the same directory and edit it as needed. Secrets are kept out of the repository by `.gitignore`.

### Input configuration

`.config/input.json`:

```json
{
  "discord_token": "",
  "discord_owner_id": "",
  "discord_enabled": false,
  "cli_enabled": true
}
```

Discord is enabled when `discord_token` is present and requires `discord_owner_id`. CLI input is enabled with `cli_enabled: true`.

### Provider configuration

`.config/provider.json`:

```json
{
  "providers": [
    {
      "name": "openrouter",
      "adapter": "openai",
      "http_endpoint": "https://openrouter.ai/api/v1",
      "api_keys": ["key-1", "key-2"]
    }
  ]
}
```

Providers may also be added from Discord with `/provider`, or from the CLI with `/provider add <name> <adapter> <url> <api-key>`.

## Supervisor

The supervisor is an implementation detail for service administration and older source-based installations:

```bash
bash scripts/supervisor.sh run
bash scripts/supervisor.sh stop
```

Normal software updates should use `ai update` rather than calling the supervisor update mode directly.

## Binary build pipeline

Every push to `main` that changes source/configuration triggers GitHub Actions. It runs the Go test suite, cross-compiles four CGO-free binaries, computes SHA-256 checksums, and commits the resulting files to `bin/`.

```text
bin/ai-linux-amd64
bin/ai-linux-arm64
bin/ai-darwin-amd64
bin/ai-darwin-arm64
bin/checksums.txt
```

The workflow ignores `bin/**` changes when triggering, so its own binary commit does not recursively rebuild.

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

The database records more than the reconstructed conversation history. It keeps sessions, turns, requests, and responses, including individual provider retry attempts.

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

The concrete transport is `transport/discord`. The single Discord user authorized to use bot interactions is configured by `discord_owner_id` in `.config/input.json`. Model and provider settings can be managed through Discord slash commands.

## Routing and model discovery

Discovered model catalogues are preferred over static routes. The model name does not implicitly choose a logical provider; the provider is part of the session configuration.

## Provider adapters

The prototype contains OpenAI, Anthropic, and Gemini wire adapters. Each adapter supports session-selected API keys and provider-specific base URL injection without mutating the shared adapter instance. Each adapter also implements live model discovery.

Key rotation is intentionally **not** implemented yet. Retries reuse the same selected key.
