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

The installer and updater download with `gh release download --repo Tulipskun/ai` and verify the binary against the release `checksums.txt`. Pass a release tag to pin a version (default: `latest`).

```bash
ai update v1.2.3
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
│   ├── browser.json
│   ├── system.json
│   └── attachment.json
├── data/
│   ├── jobs.json
│   ├── browser/
│   │   └── profile/
│   ├── sessions/
│   │   ├── <session-1>.db
│   │   ├── <session-2>.db
│   │   └── ...
│   └── attachments/
│       └── <session>/
│           ├── manifest.json
│           └── files/
│               └── <attachment-id>
├── ai.pid
└── ai.log
```

`config/entry.json` contains Discord and CLI transport settings. `config/provider.json` contains providers and API keys. `config/browser.json` contains browser automation settings. `config/system.json` contains the default provider/model, max output tokens, workspace directory, and system prompt:

```json
{
  "provider": "",
  "model": "",
  "max_output_tokens": 0,
  "workspace": "",
  "system_prompt": ""
}
```

Empty values fall back to built-in defaults (home directory for workspace, single configured provider when only one exists). There is no environment-variable configuration; `config/*.json` is the only source. Manage the prompt with `ai system`, `ai system set <prompt>`, `ai system clear`, and pin a binary with `ai update [<version>]`.

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

Browser automation is implemented directly in Go through Chrome DevTools Protocol (CDP), including Firefox ESR Remote Agent CDP compat. It does not start a Node.js worker and does not require Playwright or another browser automation library. The runtime can start a dedicated installed Chrome/Chromium/Edge/Firefox profile or attach to an already running browser through a local CDP endpoint.

The default mode is `managed` and headed:

```json
{
  "enabled": true,
  "mode": "managed",
  "headless": false,
  "browser": "chromium",
  "profile": "data/browser/profile",
  "allow_private": false,
  "idle_timeout": "30m",
  "navigation_timeout": "30s",
  "action_timeout": "10s",
  "snapshot_timeout": "10s"
}
```

`browser` may be `auto`, `chrome`, `chromium`, `edge`, or `firefox` (Chromium managed via `--remote-debugging-pipe` with no TCP listener; Firefox ESR 140 remains selectable via BiDi `/session`); the default is `chromium`, and `auto` prefers Chromium first (Firefox ESR 140 remains selectable via explicit `firefox`). Run `ai browser` to configure browser automation interactively (`ai browser disable` turns it off). The browser launches lazily on the first browser tool call, never on daemon boot or `ai update` alone. In `managed` mode the runtime starts a dedicated profile. In `attach` mode, `cdp_endpoint` points at an existing browser remote debugging endpoint:

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

## OS control tools

Worker OS-level desktop control (`tools/os_input.go`, REQ-042) alongside browser automation:

```text
os_mouse_move
os_mouse_click
os_key_press
os_type_text
os_screenshot
os_mouse_drag
os_mouse_scroll
os_window_list
os_window_focus
os_window_geometry
```

Primary backend is xdotool on X11 (requires `DISPLAY`); keyboard/text fall back to wtype on Wayland sessions (mouse/window tools return a clear keyboard-only error under wtype). `os_screenshot` captures via ImageMagick `import -root -png` and stores the PNG in the session attachment store as a file reference only (`name`, `content_type`, `size`, `ref_id`, `path`; never bytes/base64 on the text path, queued for upload like `send_attachment`). Optional `display` (e.g. `:0`), optional `name`, optional advisory `scale` within `(0,1]` with a resize note. `os_mouse_drag` holds a button from `x1,y1` to `x2,y2` with 1–50 interpolation steps (default 10). Validation clamps coordinates to 0–16384 and enforces key/text/steps/scroll/pattern/window-id/display/name limits. Set `AI_OS_INPUT_DRY_RUN=1` to validate arguments and planned argv without touching a display server.

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

### Attachment configuration

`config/attachment.json`:

```json
{
  "enabled": true,
  "root": "data/attachments",
  "max_file_bytes": 26214400,
  "max_session_bytes": 104857600,
  "ttl": "168h0m0s",
  "download_timeout": "30s",
  "upload_timeout": "2m0s",
  "max_send_file_bytes": 8388608,
  "max_send_file_count": 10
}
```

`root` is relative to the state directory and must stay inside it, so the attachment store can never land in the working tree or in `data/sessions/`. Omitted values fall back to the defaults shown (25 MiB per file, 100 MiB per session, 7 days). A negative limit, a session limit smaller than the file limit, a non-positive `ttl`, or a `root` that is present but blank is rejected when the file is loaded. The store API hands out metadata only — attachment ID, name, content type, size, and relative path — so file content never travels through a reference.

The four transfer budgets bound the Discord file boundary rather than the store: `download_timeout` for one inbound fetch, `upload_timeout` for one outbound multipart send, and `max_send_file_bytes` / `max_send_file_count` for one outbound message. They are deliberately separate from the harness display timeout, which stays a text-reply budget. Omitted or absent, each keeps the default shown.

## Discord attachments

A file a user posts in Discord is downloaded by the transport into the session's attachment store and reaches the agent only as a reference: `Input.Metadata` carries `attachment_count`, `attachment_ids`, and one `attachment_<n>_` group of `id`, `name`, `content_type`, `size`, `path` per stored file (indexes start at 1), plus `attachment_problem_count` and `attachment_problems` for files that could not be stored. The original `channel_id`, `message_id`, `author_id`, and `author_name` keys are unchanged, the canonical `Turn`/`ContentPart` contract is untouched, and a message with only attachments still carries reference text, so it never becomes an empty turn. A file that is broken, oversized, or unreachable leaves a safe note behind instead of losing the turn, and no download URL or raw error text is ever included.

Sending a file back is a transport responsibility, and `transport/discord` is the only module that uploads one. It reads three optional keys from `sdk.Output.Metadata` — `out_attachment_ids` (comma-separated store reference IDs), `out_attachments` (`manifest` or `latest`), and `out_attachment_raw` (refused by design, reported instead of ignored) — resolves them against that output's own session only, and uploads with one multipart message after the text has been delivered. A file that cannot be sent, or a request no sender can honour, is reported with a short safe indicator, never with raw REST error text, a path, or another session's identity.

Nothing outside the transport interprets those keys for upload, and the producer is the worker `send_attachment` tool (`tools/attachments.go`): it validates the opaque reference against the session store and records the intent via `sdk.RecordOutboundAttachment` (capped at 10); `HarnessLoop.Entry` (`sdk/loop.go`) drains via `TakeOutboundAttachmentIDs` and stamps `Output.Metadata[out_attachment_ids]` (merged, per-turn dedup) for the transport `SendFiles` path. File bytes never travel the SDK text path.

## CLI

The application entry point is `cmd/ai`. Build it as the `ai` command and start the Harness with:

```bash
go build -o ai ./cmd/ai
./ai cli
```

`ai start` runs the daemon in the background. `ai stop` stops the running daemon. `ai cli` runs the interactive terminal UI. `ai update` updates the installed binary through the blue-green handover only (starting the daemon first when it is not running); the single `ai` binary owns the handover end to end with no external supervisor. `ai uninstall` removes the binary and runtime state.

## Binary build pipeline

Every push to `main` triggers GitHub Actions (`.github/workflows/release.yml`). It runs the Go test suite, cross-compiles one CGO-free Linux arm64 binary, computes SHA-256 checksums, and publishes it as a GitHub Release asset (no binaries are committed to the repo).

```text
ai-linux-arm64
checksums.txt
```

`main` builds publish the next version automatically (`v1.0`, `v1.1`, ...). Pushing your own tag like `v1.5` publishes that pinned release instead. Install/update with:

```bash
gh release download --repo Tulipskun/ai --pattern 'ai-linux-arm64' --pattern checksums.txt
ai update v1.1
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

The Harness uses non-streaming Generate only; streaming mode is not supported.

## Persistent sessions and jobs

Each session is stored in its own SQLite database under `data/sessions/`. The filename is derived safely from the session ID, and the database contains only that session's settings, turns, provider request/response records, and Discord channel mappings.

`data/jobs.json` persists background jobs created by the `run_job` tool. Every job records its owning session ID. `check_job` and `close_job` can access only jobs owned by the current session, and jobs that were running when the process stopped are restored as failed instead of remaining orphaned.

## Agent loop

`Agent` is the provider-neutral control loop above `RouterClient`. It repeatedly performs model → tool calls → tool results → model until the model returns no tool calls. Tool failures are returned to the model as `tool_result` entries with `IsError=true`, allowing the model to recover instead of crashing the whole turn.

## Web fetch and browser automation

`web_fetch` is the lightweight path for static HTTP/HTTPS pages, documentation, and APIs. Browser automation is a built-in Go CDP tool with no Node.js or Playwright runtime dependency.

## Attachment tools

The worker registry also carries three read-only tools for the attachment file store: `list_attachments` (no arguments; the current session's references — id, name, content type, size, relative path — and no content), `read_attachment` (`ref_id`, optional `max_bytes`; text-like attachments only, capped at 2 MiB read and 20 000 characters with a `truncated` flag, and a clear refusal that names the content type and size for binary payloads), and `describe_attachment` (`ref_id`; metadata analysed with the standard library only — PNG/JPEG/GIF dimensions, and for PDFs a page count plus best-effort text from FlateDecode content streams).

Every one of them resolves the session from the tool context, never from a model-supplied argument, so a session cannot read another session's attachments, and all path, traversal, and symlink checks stay inside the store. Results are always bounded text plus a file reference: raw bytes and base64 never enter the canonical SDK text path. Sending files is a transport responsibility and is not implemented in `tools/`. The tools report `attachment store is not configured` when no store is injected.

## Input and display architecture

The Harness core is transport-independent. Input sources convert external events into canonical `Input` values, while displays consume canonical `Output` values. Discord and CLI can be enabled at the same time and share the same Harness/Agent runtime.

## Discord runtime

The concrete transport is `transport/discord`. The authorized Discord owner is configured in `config/entry.json`. Model and provider settings can be managed through Discord slash commands.

## Provider adapters

OpenAI Responses (`sdk/providers/openai`) is the central wire interface. `BuildResponsesRequest` produces the canonical request map, and `ParseResponsesResponse` / `ResponsesResponseFromParts` produce the canonical `sdk.Response`. Anthropic and Gemini adapters convert through it (`BuildFromOpenAI` on the request path, `ToOpenAIResponse` on the response path) instead of translating `sdk.Request` directly. Each adapter supports session-selected API keys and provider-specific base URL injection without mutating the shared adapter instance. Each adapter also implements live model discovery. Chat Completions is kept only as a fallback for providers that reject `/responses`.

Key rotation is intentionally not implemented yet; retries reuse the same selected key.
