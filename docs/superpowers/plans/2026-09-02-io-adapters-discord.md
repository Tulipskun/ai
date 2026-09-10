# Input/Display Adapters with Discord

## Goal

Separate the Harness core loop from transport-specific input and display so Discord is the first adapter, while future Web/Console/Telegram adapters can be added without changing the AI/session core.

## Invariants

1. Input is committed to Session before an AI request starts.
2. Successful AI response is committed to Session before display is attempted.
3. Display is a side effect and must never block or fail the core loop.
4. Display failures are observed/logged but do not rollback session state or stop input processing.
5. Core loop depends only on canonical input/output abstractions; it does not import Discord.
6. Discord input/output is an adapter implementation only.
7. Retry continues to use the selected session key; no rotation is introduced.
8. Session history repair remains owned by Session/turn transaction logic.

## Files

- `sdk/io.go`: canonical `Input`, `Output`, `InputSource`, `Display`, and safe asynchronous display dispatch.
- `sdk/io_test.go`: tests for canonical IO and non-blocking/failure-isolated display dispatch.
- `sdk/loop.go`: core input-to-AI orchestration; session commit happens before request and response commit happens before display dispatch.
- `sdk/loop_test.go`: transaction/order tests using fake input/display implementations.
- `sdk/discord.go`: Discord-specific adapter boundary, keeping Discord transport details outside the core loop.
- `sdk/discord_test.go`: Discord adapter normalization tests without requiring a live Discord connection.
- `README.md`: document the source/display architecture and invariants.

## TDD sequence

1. Add failing tests for display failure isolation and input/response session ordering.
2. Verify the tests fail for the intended missing behavior.
3. Add the minimal canonical IO and loop abstractions.
4. Add Discord adapter normalization.
5. Run the complete Go test suite and `go vet`.
6. Refactor only after tests are green.

## Non-goals

- No key rotation.
- No persistent session database.
- No Discord Gateway implementation or Discord token management inside the SDK core.
- No multi-display delivery guarantees beyond best-effort asynchronous dispatch.
