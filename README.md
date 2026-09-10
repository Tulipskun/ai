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

Runtime configuration is file-based and stored separately from executable files.

```text
~/.local/share/ai/
├── config/
│   ├── entry.json
│   ├── provider.json
│   └── browser.json
├── data/
│   ├── jobs.json
│   ├── browser/
│   │   └── profile/
│   └── sessions/
│       ├── <session-1>.db
│       ├── <session-2>.db
│       └── ...
├── ai.pid
└── ai.log
```

`config/entry.json` contains Discord and CLI transport settings. `config/provider.json` contains providers and API keys. `config/browser.json` contains browser automation settings.

### Discord configuration

`config/entry.json`:

```json
{
  "discord": {
    "enabled": true,
    "token": "",
    "owner_id": ""
  },
  "cli": {
    "enabled": true
  }
}
```

### Browser configuration

Browser automation is implemented directly in Go through Chrome DevTools Protocol. It does not start a Node.js worker and does not require Playwright or another browser automation library. The runtime can start a dedicated installed Chrome/Chromium/Edge profile or attach to an already running browser through a local CDP endpoint.

The default mode is `managed` and headed:

```json
{
  "enabled": true,
  "mode": "managed",
  "headless": false,
  "browser": "auto",
  "profile": "data/browser/profile",
  "allow_private": false,
  "idle_timeout": "30m",
  "navigation_timeout": "30s",
  "action_timeout": "10s",
  "snapshot_timeout": "10s"
}
```

`browser` may be `auto`, `chrome`, `chromium`, or `edge`. In `managed` mode the runtime starts a dedicated profile. In `attach` mode, `cdp_endpoint` points at an existing browser remote debugging endpoint:

```json
{
  "enabled": true,
  "mode": "attach",
  "headless": false,
  "cdp_endpoint": "http://127.0.0.1:9222"
}
```

The Agent is exposed to these browser tools:

```text
browser_list_pages
browser_attach
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

## Provider configuration

`config/provider.json`:

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

`ai start` runs the daemon in the background. `ai cli` runs the interactive terminal UI. `ai update` updates the installed binary and restarts the daemon only when it was already running. `ai uninstall` removes the binary and runtime state.

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

Streaming is retried only when the failure happens before the stream has emitted an event. Once output has started, the stream is never replayed automatically because replaying it could duplicate user-visible output.

## Persistent sessions and jobs

Each session is stored in its own SQLite database under `data/sessions/`. The filename is derived safely from the session ID, and the database contains only that session's settings, turns, provider request/response records, and Discord channel mappings.

`data/jobs.json` persists background jobs created by the `run_job` tool. Every job records its owning session ID. `check_job` and `close_job` can access only jobs owned by the current session, and jobs that were running when the process stopped are restored as failed instead of remaining orphaned.

## Agent loop

`Agent` is the provider-neutral control loop above `RouterClient`. It repeatedly performs model → tool calls → tool results → model until the model returns no tool calls. Tool failures are returned to the model as `tool_result` entries with `IsError=true`, allowing the model to recover instead of crashing the whole turn.

## Web fetch and browser automation

`web_fetch` is the lightweight path for static HTTP/HTTPS pages, documentation, and APIs. Browser automation is a built-in Go CDP tool with no Node.js or Playwright runtime dependency.

## Input and display architecture

The Harness core is transport-independent. Input sources convert external events into canonical `Input` values, while displays consume canonical `Output` values. Discord and CLI can be enabled at the same time and share the same Harness/Agent runtime.

## Discord runtime

The concrete transport is `transport/discord`. The authorized Discord owner is configured in `config/entry.json`. Model and provider settings can be managed through Discord slash commands.

## Provider adapters

The prototype contains OpenAI, Anthropic, and Gemini wire adapters. Each adapter supports session-selected API keys and provider-specific base URL injection without mutating the shared adapter instance. Each adapter also implements live model discovery.

Key rotation is intentionally not implemented yet; retries reuse the same selected key.
