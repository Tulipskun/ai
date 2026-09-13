# Tulipskun/ai

Prototype canonical Go SDK for an AI Harness.

## Install

Install the `ai` command on Linux arm64 with one command. The installer downloads only the `ai-linux-arm64` binary from GitHub Releases (`gh release download`); it does not require Git or Go. Runtime state is kept under `~/.local/share/ai` by default.

```bash
curl -fsSL https://raw.githubusercontent.com/Tulipskun/ai/main/scripts/install.sh | bash
```

Then start it with:

```bash
ai start
```

GitHub Actions builds the Linux arm64 binary and publishes it as a GitHub Release asset after source changes.

```bash
ai update
```

The installer and updater download with `gh release download --repo Tulipskun/ai` and verify the binary against the release `checksums.txt`. `AI_VERSION` can be set to a release tag when a pinned version is required (default: `latest`).

```bash
AI_VERSION=v1.2.3 ai update
```

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

`browser` may be `auto`, `chrome`, `chromium`, or `edge`. Run `ai browser` to configure browser automation interactively (`ai browser disable` turns it off). In `managed` mode the runtime starts a dedicated profile. In `attach` mode, `cdp_endpoint` points at an existing browser remote debugging endpoint:

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

Providers may also be added from Discord with `/provider`, or from the CLI with `/provider add <name> <adapter> <url> <api-key> [free]`. Append `free` (or set `"free_only": true` in `config/provider.json`) to keep only `-free` models in discovery. Extra per-provider HTTP headers go in `"headers"` (for example `"headers": {"HTTP-Referer": "https://example.com", "X-Title": "my-app"}`); auth headers always win over custom ones.

## CLI

The application entry point is `cmd/ai`. Build it as the `ai` command and start the Harness with:

```bash
go build -o ai ./cmd/ai
./ai cli
```

`ai start` runs the daemon in the background. `ai stop` stops the running daemon (including the keepalive watcher so it does not restart). `ai cli` runs the interactive terminal UI. `ai update` updates the installed binary and restarts the daemon only when it was already running. `ai uninstall` removes the binary and runtime state.

## Binary build pipeline

Every push to `main` triggers GitHub Actions (`.github/workflows/release.yml`). It runs the Go test suite, cross-compiles one CGO-free Linux arm64 binary, computes SHA-256 checksums, and publishes it as a GitHub Release asset (no binaries are committed to the repo).

```text
ai-linux-arm64
checksums.txt
```

`main` builds publish the next version automatically (`v1.0`, `v1.1`, ...). Pushing your own tag like `v1.5` publishes that pinned release instead. Install/update with:

```bash
gh release download --repo Tulipskun/ai --pattern 'ai-linux-arm64' --pattern checksums.txt
AI_VERSION=v1.1 ai update
```

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

OpenAI Responses (`sdk/providers/openai`) is the central wire interface. `BuildResponsesRequest` produces the canonical request map, and `ParseResponsesResponse` / `ResponsesResponseFromParts` produce the canonical `sdk.Response`. Anthropic and Gemini adapters convert through it (`BuildFromOpenAI` on the request path, `ToOpenAIResponse` on the response path) instead of translating `sdk.Request` directly. Each adapter supports session-selected API keys and provider-specific base URL injection without mutating the shared adapter instance. Each adapter also implements live model discovery. Chat Completions is kept only as a fallback for providers that reject `/responses`.

Key rotation is intentionally not implemented yet; retries reuse the same selected key.
