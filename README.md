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

GitHub Actions builds Linux amd64/arm64 and macOS amd64/arm64 binaries and commits them to `bin/` after source changes.

```bash
ai update
```

The installer and updater verify the downloaded binary against `bin/checksums.txt`. `AI_VERSION` can be set to a branch, tag, or commit ref when a pinned repository version is required.

To completely remove the installation, including the daemon, runtime state, sessions, provider configuration, logs, and history, run:

```bash
ai uninstall
```

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
│   ├── cli.history
│   └── browser/
│       └── profile/
└── .ai/
    ├── ai.pid
    └── ai.log
```

`.config/provider.json` contains providers and API keys. `.config/input.json` contains transport settings. `.config/browser.json` contains browser automation settings. Example files are provided in `.config/`.

### Input configuration

```json
{
  "discord_token": "",
  "discord_owner_id": "",
  "discord_enabled": false,
  "cli_enabled": true
}
```

### Browser configuration

Browser automation is built into the Go runtime. It does not start a Node.js worker or require Playwright to be installed. The runtime finds an installed Chrome, Chromium, or Edge binary, starts a dedicated browser profile, and communicates with it through Chrome DevTools Protocol.

The default mode is headed so the browser window is visible to the user:

```json
{
  "enabled": true,
  "headless": false,
  "browser": "auto",
  "profile": ".data/browser/profile",
  "allow_private": false,
  "idle_timeout": "30m",
  "navigation_timeout": "30s",
  "action_timeout": "10s",
  "snapshot_timeout": "10s"
}
```

`browser` may be `auto`, `chrome`, `chromium`, or `edge`. In `auto` mode the runtime searches the installed browser executables. A dedicated profile is used so the AI browser does not take over the user's normal browser profile. On Linux, headed mode requires an available graphical session (`DISPLAY`/Wayland environment).

The browser is exposed to the Agent as built-in tools:

```text
browser_open
browser_close
browser_navigate
browser_snapshot
browser_click
browser_fill
browser_press
browser_select
browser_scroll
browser_get_text
browser_screenshot
```

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

## CLI

The application entry point is `cmd/ai`. Build it as the `ai` command and start the Harness with:

```bash
go build -o ai ./cmd/ai
./ai cli
```

`ai start` runs the daemon in the background. `ai cli` runs the interactive terminal UI. `ai update` updates the installed binary and restarts the daemon. `ai uninstall` removes the binary and runtime state.

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

Sessions can be backed by a `.db` file instead of keeping history only in memory. The database uses SQLite WAL mode for concurrent readers and transactional updates.

## Agent loop

`Agent` is the provider-neutral control loop above `RouterClient`. It repeatedly performs model → tool calls → tool results → model until the model returns no tool calls or the hard iteration limit is reached. Tool failures are returned to the model as `tool_result` entries with `IsError=true`, allowing the model to recover instead of crashing the whole turn.

## Web fetch and browser automation

`web_fetch` is the lightweight path for static HTTP/HTTPS pages, documentation, and APIs. Browser automation is a built-in native CDP tool and runs in headed mode by default against an installed Chrome/Chromium/Edge browser.

## Input and display architecture

The Harness core is transport-independent. Input sources convert external events into canonical `Input` values, while displays consume canonical `Output` values. Discord and CLI can be enabled at the same time and share the same Harness/Agent runtime.

## Discord runtime

The concrete transport is `transport/discord`. The single Discord user authorized to use bot interactions is configured by `discord_owner_id` in `.config/input.json`. Model and provider settings can be managed through Discord slash commands.

## Provider adapters

The prototype contains OpenAI, Anthropic, and Gemini wire adapters. Each adapter supports session-selected API keys and provider-specific base URL injection without mutating the shared adapter instance. Each adapter also implements live model discovery.

Key rotation is intentionally **not** implemented yet. Retries reuse the same selected key.
