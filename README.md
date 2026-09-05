# Tulipskun/ai

Prototype canonical Go SDK for an AI Harness.

The SDK keeps conversation history in a provider-neutral format and translates it at the provider boundary. It separates the **logical provider** used by a session from the **adapter** used to speak a provider API.

## Retry policy

Retries are deliberately separate from key rotation. The selected session key never changes during a retry.

The default policy is 3 attempts with exponential backoff, capped at 4 seconds. HTTP 404 is the only explicitly non-retryable HTTP status. Provider-specific 400 responses may therefore be retried. Context cancellation and deadline errors are not retried.

Streaming is retried only when the failure happens before the stream has emitted an event. Once output has started, the stream is never replayed automatically because replaying it could duplicate user-visible output.

## Discord authorization

Discord interactions are restricted to the single configured owner ID. Set `DISCORD_OWNER_ID` to the Discord user ID allowed to use the model, provider, and stop commands. Discord is not started without both `DISCORD_BOT_TOKEN` and `DISCORD_OWNER_ID`.

## Persistent SQLite sessions

Sessions can be backed by a `.db` file instead of keeping history only in memory. Session history and request/response telemetry remain provider-neutral and durable.

## Agent loop

`Agent` is the provider-neutral control loop above `RouterClient`. It repeatedly performs model → tool calls → tool results → model until the model returns no tool calls or the hard iteration limit is reached. Tool failures are returned to the model as `tool_result` entries with `IsError=true`.

The coding registry exposes filesystem, command, asynchronous job, web fetch, and browser tools. File operations are restricted to the configured workspace root, including symlink-aware path validation. `edit_file` requires exactly one match for `old_text`.

`run_command` is synchronous. For long-running work, `run_job` starts the command asynchronously and returns a job ID. `check_job` can inspect state/output and `close_job` can terminate a running job. Job metadata is persisted under `.ai/jobs/jobs.json`; jobs that were running when the process stopped are restored as failed because their OS process cannot be safely resumed. Captured output is bounded.

## Web fetch and browser automation

`web_fetch` is the lightweight path for static HTTP/HTTPS pages, documentation, and APIs. Browser automation uses Playwright with Chromium in a separate Node.js worker.

Web access has SSRF protection by default. The Go web-fetch policy and browser worker use the same blocked private/local/link-local/metadata-style network ranges. Set `AI_BROWSER_ALLOW_PRIVATE=true` only when access to private network services is intentionally required.

## Streaming provider state

Provider streaming events preserve tool-call arguments and reasoning state. Anthropic partial tool JSON is accumulated per content block, while OpenAI reasoning-summary and Gemini thought parts are surfaced through the canonical `EventReasoning` event. `StreamTurn()` persists reasoning state together with the model turn after a successful stream.

The remaining transport and architecture documentation below continues to describe the canonical Harness design.
