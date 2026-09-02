# Multi-Transport Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the existing transport-independent Harness core into a runnable multi-transport runtime, with Discord as the first real transport adapter while keeping Telegram, Web, and future transports outside the core.

**Architecture:** The SDK owns only canonical input/output, sessions, AI execution, and transport-neutral lifecycle contracts. Concrete transports live outside `sdk`; each adapter translates its native events into `sdk.Input` and sends `sdk.Output` back to the originating transport. Output routing is source-aware so a Discord response cannot accidentally be sent to Telegram/Web, and future adapters require no Harness changes.

**Tech Stack:** Go 1.25, existing `sdk` Harness/Session/Agent/Router, `runtime` provider loader, SQLite session persistence, `github.com/disgoorg/disgo` v0.19.6 for Discord Gateway/REST.

**Spec:** `docs/superpowers/plans/2026-09-02-multi-transport-runtime.md`

## Global Constraints

- Go version remains `1.25.0`.
- Provider configuration remains provider-only: provider name, HTTP endpoint, and API key array; transport credentials are separate.
- Discord must remain an adapter; Discord library types must not leak into `sdk`.
- Telegram/Web are not implemented in this change, but their adapters must be able to implement the same transport contract without modifying `sdk` or `HarnessLoop`.
- Session history remains canonical/provider-neutral and durable through the existing session implementation.
- Display/output side effects must not block the Harness processing loop.
- Same-session turns must not execute concurrently and corrupt/interleave history; different sessions may execute concurrently.
- No secrets are committed to Git.

---

### Task 1: Establish the transport-neutral routing contract

**Files:**
- Modify: `sdk/io.go`
- Modify: `sdk/loop.go`
- Create: `sdk/io_test.go`
- Create: `sdk/loop_transport_test.go`

**Interfaces:**
- Consumes: existing `sdk.Input`, `sdk.Output`, `InputSource`, `Display`, and `HarnessLoop`.
- Produces: a source-aware output routing contract and serialized per-session turn execution.

- [ ] **Step 1: Write failing tests for source-aware output routing**

```go
func TestOutputRouterSendsOnlyToMatchingSource(t *testing.T) {
    // Register one sink for discord and one for telegram.
    // Dispatch a discord Output and assert only discord receives it.
}
```

Also add a test proving two `Handle` calls with the same `SessionID` execute serially, while different session IDs can proceed independently.

- [ ] **Step 2: Run the focused tests and verify they fail**

Run: `go test ./sdk -run 'TestOutputRouter|TestHarness.*Session' -count=1`
Expected: FAIL because the source-aware router/session serialization contract does not yet exist.

- [ ] **Step 3: Implement the minimal routing/serialization layer**

Add a transport-neutral `OutputSink`/router abstraction in `sdk/io.go`. The router must select sinks by `Output.Source`, and the loop must guard each `SessionID` with a per-session lock or equivalent serialization mechanism. Keep the existing asynchronous display isolation semantics.

- [ ] **Step 4: Run the focused tests and verify they pass**

Run: `go test ./sdk -run 'TestOutputRouter|TestHarness.*Session' -count=1`
Expected: PASS.

- [ ] **Step 5: Run the full SDK tests**

Run: `go test ./sdk -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add sdk/io.go sdk/loop.go sdk/io_test.go sdk/loop_transport_test.go
git commit -m "feat: add source-aware transport routing"
```

### Task 2: Move Discord out of the SDK and preserve normalization tests

**Files:**
- Create: `transport/discord/adapter.go`
- Create: `transport/discord/adapter_test.go`
- Delete: `sdk/discord.go`
- Modify: existing Discord adapter tests so they import `transport/discord` instead of `sdk`

**Interfaces:**
- Consumes: `sdk.Input`, `sdk.Output`, `sdk.InputSource`, and the transport-neutral output sink contract from Task 1.
- Produces: `transport/discord` adapter with no Discord-specific symbols in `sdk`.

- [ ] **Step 1: Write failing package-boundary tests**

```go
func TestDiscordMessageNormalizesToSDKInput(t *testing.T) {
    // Build a native Discord DTO and assert Source, SessionID, user text,
    // and routing metadata are normalized into sdk.Input.
}
```

Add a compile-level/package test ensuring the Discord adapter is the only package importing the Discord library.

- [ ] **Step 2: Run focused tests and verify the new package fails**

Run: `go test ./transport/discord -count=1`
Expected: FAIL until the adapter package exists.

- [ ] **Step 3: Implement the adapter boundary**

Move `DiscordInputMessage`, normalization, input source, and sender/display behavior into `transport/discord`. The adapter may depend on the Discord library; `sdk` must not. Preserve `channel_id`, `message_id`, `author_id`, and `author_name` as transport metadata.

- [ ] **Step 4: Run focused tests**

Run: `go test ./transport/discord -count=1`
Expected: PASS.

- [ ] **Step 5: Run all tests**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add sdk/ transport/discord/
git commit -m "refactor: isolate Discord transport adapter"
```

### Task 3: Add the real Discord Gateway adapter

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `transport/discord/adapter.go`
- Create: `transport/discord/gateway.go`
- Create: `transport/discord/gateway_test.go`

**Interfaces:**
- Consumes: Discord bot token, `sdk.InputSource`, and source-aware output sink contract.
- Produces: a concrete Discord transport that opens the Gateway, ignores bot-authored messages, converts message-create events into canonical input, and sends canonical text output through Discord REST.

- [ ] **Step 1: Add the Discord dependency**

Use `github.com/disgoorg/disgo` v0.19.6. Its module declares Go 1.24 compatibility, so it fits the repository's Go 1.25 floor; its Gateway package targets Discord Gateway v10. citeturn2view0turn1search0

- [ ] **Step 2: Write failing Gateway tests**

Cover these cases:

```go
func TestGatewayIgnoresBotMessages(t *testing.T) {}
func TestGatewayBuildsSessionIDFromConversation(t *testing.T) {}
func TestGatewayUsesMessageChannelAsReplyTarget(t *testing.T) {}
func TestGatewayRequiresMessageContentIntent(t *testing.T) {}
```

Use a fake native-event-to-message seam so tests do not require a live Discord connection.

- [ ] **Step 3: Implement Gateway lifecycle**

Create a `transport/discord.Client` that owns the DisGo client and exposes `Start(ctx)`, `Close(ctx)`, `Receive(ctx)`, and `Send(ctx, Output)` through transport-neutral behavior. Configure Gateway intents required for normal message handling, including guild messages and message content. DisGo's documented example uses `IntentGuildMessages` and `IntentMessageContent` for message events. citeturn1search12

- [ ] **Step 4: Implement session identity**

Default Discord session identity to a conversation key that is stable across turns, such as `discord:channel:<channel_id>`. Keep the identity policy injectable so a future Telegram/Web adapter can choose `telegram:chat:<id>` or `web:conversation:<id>` without changing the Harness.

- [ ] **Step 5: Implement outbound sending**

Route an `sdk.Output` only to the Discord sink when `Output.Source == "discord"`, extract the channel ID from normalized routing metadata, concatenate text content parts, and send through Discord REST. Do not send empty responses.

- [ ] **Step 6: Run focused tests**

Run: `go test ./transport/discord -count=1`
Expected: PASS without requiring a Discord token.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum transport/discord/
git commit -m "feat: add Discord Gateway transport"
```

### Task 4: Add transport configuration and runnable runtime wiring

**Files:**
- Create: `transport/config.go`
- Create: `transport/config_test.go`
- Create: `cmd/ai/main.go`
- Create: `.config/transport.example.json`
- Modify: `.gitignore`
- Modify: `README.md`

**Interfaces:**
- Consumes: existing `.config/provider.json`, transport environment/config, `runtime.Runtime`, `sdk.HarnessLoop`, and Discord transport.
- Produces: one runnable process that can start the AI runtime plus one or more enabled transports without coupling the core to Discord.

- [ ] **Step 1: Write failing config tests**

```go
func TestTransportConfigReadsDiscordTokenFromEnvironment(t *testing.T) {}
func TestTransportConfigDoesNotPersistSecrets(t *testing.T) {}
func TestDisabledTransportsAreNotStarted(t *testing.T) {}
```

- [ ] **Step 2: Implement transport config**

Keep secrets in environment variables. The example configuration describes enabled transports and non-secret options; the Discord token is read from `DISCORD_BOT_TOKEN`. Provider keys remain in `.config/provider.json` and are not mixed with transport credentials.

- [ ] **Step 3: Implement runtime wiring**

The process must load provider runtime, refresh the live model catalogue, construct the selected session configuration, create the transport adapters, and run the common Harness loop. The Harness must receive `sdk.Input` and emit `sdk.Output`; no Discord types may appear in `cmd/ai/main.go` beyond adapter construction.

- [ ] **Step 4: Define model selection explicitly**

Do not silently pick an arbitrary model from the live catalogue. The runtime must require an explicit model setting for the first runnable configuration, validate it against the refreshed provider catalogue, and construct `sdk.SessionConfig` from provider/model/key/thinking/temperature settings.

- [ ] **Step 5: Add graceful shutdown**

Use a process context cancelled by SIGINT/SIGTERM. Stop input sources and close transports without blocking the Harness's output side-effect goroutines indefinitely.

- [ ] **Step 6: Run config and full tests**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add transport cmd .config/transport.example.json .gitignore README.md
git commit -m "feat: add runnable multi-transport runtime"
```

### Task 5: Verify end-to-end contracts and document future adapters

**Files:**
- Create: `transport/runtime_test.go`
- Modify: `README.md`
- Modify: `docs/superpowers/plans/2026-09-02-multi-transport-runtime.md` only if implementation details changed materially

**Interfaces:**
- Consumes: all transport/core components from Tasks 1-4.
- Produces: verified multi-transport behavior and adapter extension documentation.

- [ ] **Step 1: Add an end-to-end fake-transport test**

Feed two independent fake sources into the same Harness: one with `Source == "discord"` and one with `Source == "web"`. Assert that each response returns only to its matching sink and that both can use the same Harness implementation.

- [ ] **Step 2: Verify same-session ordering**

Send two messages with the same `SessionID` concurrently and assert the persisted canonical history is ordered user → model → user → model without interleaving.

- [ ] **Step 3: Run the complete verification suite**

Run:

```bash
go test ./...
go vet ./...
```

Expected: both commands exit 0.

- [ ] **Step 4: Inspect the final dependency and package boundary**

Verify that `sdk` has no import of `github.com/disgoorg/disgo`, Telegram packages, HTTP/WebSocket transport packages, or other concrete channel libraries. Only transport adapter packages may import those dependencies.

- [ ] **Step 5: Update README architecture**

Document the final topology:

```text
Discord ───────┐
Telegram ──────┤
Web ───────────┤→ InputSource → HarnessLoop → Session → AI
Console ───────┘                                  ↓
                                      OutputRouter
                                           ↓
                               Discord / Telegram / Web / ...
```

Document how to add a future adapter without modifying `sdk`.

- [ ] **Step 6: Commit verification/docs**

```bash
git add transport/runtime_test.go README.md
git commit -m "docs: document multi-transport runtime"
```

### Final Verification

- [ ] Run `go test ./...` and record the passing result.
- [ ] Run `go vet ./...` and record the passing result.
- [ ] Confirm the branch contains no secrets.
- [ ] Confirm Discord-specific imports are outside `sdk`.
- [ ] Confirm the existing provider/session/agent behavior remains green.
- [ ] Confirm the runtime can start with Discord disabled, proving transport-independent core startup.
- [ ] Confirm the runtime can start with Discord enabled when a valid `DISCORD_BOT_TOKEN` is supplied, without changing provider configuration semantics.
