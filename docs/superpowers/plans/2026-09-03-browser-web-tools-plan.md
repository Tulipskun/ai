# Browser and Web Tools Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `web_fetch` plus Playwright/Chromium browser automation to the Go Agent without leaking browser-specific logic into the Agent loop.

**Architecture:** `web_fetch` runs directly in Go. Browser tools run through a localhost-only authenticated JSON-RPC Node.js worker using Playwright and one shared Chromium browser with one isolated BrowserContext per AI session. The existing `tools.Registry` remains the Agent-facing boundary.

**Tech Stack:** Go 1.25, existing `sdk.ToolExecutor`, `net/http`, Node.js, Playwright, Chromium, JSON-RPC over localhost HTTP, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-03-browser-web-tools-design.md`

## Global Constraints

- HTTP/HTTPS only for web access; `file://` navigation is disabled.
- Browser worker binds only to `127.0.0.1` and authenticates every RPC request with a startup-generated bearer token.
- One AI `Session` maps to one isolated Playwright `BrowserContext`; one Chromium browser instance is shared across contexts.
- `browser_snapshot` is the primary observation mechanism; action references are valid only for the snapshot that produced them.
- Both fetch and browser navigation block loopback, private, link-local, IPv6 local/private, and cloud-metadata-style destinations by default.
- URL policy validates resolved destination IPs and every redirect, with DNS-rebinding protection.
- V1 excludes arbitrary JavaScript/eval, raw CDP, arbitrary filesystem access, cookie export/import, and arbitrary downloads/uploads.
- Browser setup installs only Chromium and runs as root without `sudo`.
- Existing Go CI must continue to run `go test ./...` and `go vet ./...`.
- External webpage content is untrusted data and must not be treated as system or tool instructions.

---

## File Map

Create:
- `tools/web_fetch.go` — Go HTTP fetch implementation, extraction, limits, and URL-policy integration.
- `tools/web_fetch_test.go` — fetch behavior, redirects, limits, and SSRF tests.
- `tools/browser_client.go` — Go-side browser-worker RPC client, lifecycle, authentication, timeouts, and session-facing methods.
- `tools/browser_client_test.go` — RPC protocol, authentication, timeout, and lifecycle tests.
- `tools/browser_tools.go` — Agent-facing browser tool handlers and schemas.
- `tools/browser_tools_test.go` — browser tool argument/result mapping and stale-reference behavior.
- `tools/network_policy.go` — shared URL/IP validation and DNS-rebinding-safe dialing policy.
- `tools/network_policy_test.go` — private/loopback/link-local/metadata blocking and allowed public destinations.
- `browser/package.json` — Node worker dependency manifest with Playwright.
- `browser/package-lock.json` — locked Node dependencies.
- `browser/server.mjs` — authenticated HTTP server exposing `/health` and `/rpc`.
- `browser/playwright.mjs` — browser/context/page/session manager and snapshot/ref implementation.
- `browser/server.test.mjs` — worker protocol, health, authentication, and basic browser integration tests.
- `scripts/setup-browser.sh` — root/no-sudo Node/Playwright/Chromium setup and launch health check.
- `.github/workflows/browser.yml` — browser-worker CI job with Chromium installation and integration tests.

Modify:
- `tools/registry.go` — register `web_fetch` and all V1 `browser_*` definitions/handlers and provide shared worker/policy dependencies.
- `tools/registry_test.go` — update definition count and assert all V1 names exist.
- `runtime/runtime.go` — construct/own browser runtime configuration and worker lifecycle where appropriate without putting browser logic into `sdk.Agent`.
- `cmd/ai/main.go` — load browser settings, initialize the registry/worker, and close the worker on shutdown.
- `.config/transport.example.env` — document browser-related environment variables using the repository's existing env-based runtime configuration rather than introducing a new YAML dependency.
- `.gitignore` — ignore only generated browser runtime/profile state if the implementation creates any local state.
- `README.md` — document setup, configuration, tool selection, security defaults, and V1 exclusions.

The implementation must first inspect the exact current runtime/config conventions before changing `runtime/runtime.go`; do not introduce a second unrelated configuration system.

---

### Task 1: Add shared network security policy

**Files:**
- Create: `tools/network_policy.go`
- Test: `tools/network_policy_test.go`

**Interfaces:**
- Produces `NetworkPolicy` with `AllowPrivate bool` and `ValidateURL(context.Context, *url.URL) error`.
- Produces a DNS-safe HTTP transport/dial path that resolves a destination and rejects disallowed IPs before connection.
- Produces redirect validation usable by `web_fetch`.
- Browser navigation must call the same hostname/IP validation before RPC is sent to Playwright.

- [ ] **Step 1: Write failing tests**

Test these cases with `httptest`/custom resolvers where practical: `http://127.0.0.1`, `http://10.0.0.1`, `http://172.16.0.1`, `http://192.168.1.1`, `http://169.254.169.254`, `http://[::1]`, private IPv6, an ordinary public literal IP, non-HTTP schemes, and a hostname whose resolver returns a private address. Verify private access is rejected when `AllowPrivate=false` and accepted when explicitly enabled.

- [ ] **Step 2: Run the focused test and verify failure**

Run: `go test ./tools -run 'TestNetworkPolicy' -v`
Expected: FAIL because the policy types/functions do not yet exist.

- [ ] **Step 3: Implement the minimal policy**

Use `net/url`, `net`, and a resolver-aware dial path. Normalize and validate scheme/hostname, resolve hostnames, reject all disallowed resolved IPs, and revalidate redirect targets. Do not rely only on string matching. Keep the policy independent of the browser worker.

- [ ] **Step 4: Run focused tests**

Run: `go test ./tools -run 'TestNetworkPolicy' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tools/network_policy.go tools/network_policy_test.go
git commit -m "feat: add shared web network security policy"
```

---

### Task 2: Implement `web_fetch`

**Files:**
- Create: `tools/web_fetch.go`
- Test: `tools/web_fetch_test.go`

**Interfaces:**
- Consumes `NetworkPolicy` from Task 1.
- Produces handler compatible with the existing `type handler func(context.Context, json.RawMessage) (string, error)`.
- Input: `{ "url": string }`.
- Output JSON: `url`, `status`, `content_type`, `title`, `text`, `truncated`.

- [ ] **Step 1: Write failing tests**

Create local HTTP test servers for: HTML title/text extraction, plain text, redirect to another public test endpoint, oversized response truncation, invalid scheme, blocked loopback/private destination, and request timeout. Assert raw HTML is not returned as the primary `text` field.

- [ ] **Step 2: Run focused tests**

Run: `go test ./tools -run 'TestWebFetch' -v`
Expected: FAIL because the fetch handler does not exist.

- [ ] **Step 3: Implement fetch**

Use `net/http` with explicit timeout and response-size limits. Validate the initial URL and every redirect through Task 1. Read at most the configured response cap, extract readable text and title from HTML, preserve plain-text/API bodies, and mark `truncated=true` when the text/body limit is reached. Return structured JSON as the tool result.

- [ ] **Step 4: Run focused tests**

Run: `go test ./tools -run 'TestWebFetch' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tools/web_fetch.go tools/web_fetch_test.go
git commit -m "feat: add web fetch tool"
```

---

### Task 3: Build the Node Playwright worker

**Files:**
- Create: `browser/package.json`
- Create: `browser/package-lock.json`
- Create: `browser/server.mjs`
- Create: `browser/playwright.mjs`
- Create: `browser/server.test.mjs`

**Interfaces:**
- `GET /health` returns readiness information without exposing the bearer token.
- `POST /rpc` accepts `{id, method, params}` and returns `{id, ok, result}` or `{id, ok:false, error:{code,message}}`.
- Supported methods: `browser.open`, `browser.close`, `browser.navigate`, `browser.snapshot`, `browser.click`, `browser.fill`, `browser.press`, `browser.select`, `browser.scroll`, `browser.get_text`, `browser.screenshot`.
- Worker binds to `127.0.0.1` only and requires `Authorization: Bearer <startup-token>` for `/rpc`.
- `browser_open` creates/reuses one BrowserContext per `session_id` and creates a new Page when called on an existing context.
- Snapshot refs are scoped to a snapshot and stale refs return a typed error.

- [ ] **Step 1: Write failing worker tests**

Test unauthenticated RPC rejection, authenticated health/RPC success, unknown method error, opening two pages in one session, isolation between two sessions, navigation to a local integration page, snapshot returning refs, clicking/filling by refs, and stale-ref rejection after DOM replacement.

- [ ] **Step 2: Run worker tests and verify failure**

Run: `cd browser && npm test`
Expected: FAIL because the worker files/dependency are not implemented.

- [ ] **Step 3: Add Playwright and implement the worker**

Use Playwright's Chromium API. Keep one browser instance per worker process and maintain `Map<session_id, context>` plus page state. Normalize snapshot output into page metadata plus accessible elements with generated refs. Do not expose `eval`, CDP, filesystem, cookies, or downloads. Enforce action/navigation/snapshot timeouts and idle cleanup.

- [ ] **Step 4: Run worker tests**

Run: `cd browser && npm test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add browser/
git commit -m "feat: add Playwright browser worker"
```

---

### Task 4: Add the Go browser-worker RPC client

**Files:**
- Create: `tools/browser_client.go`
- Test: `tools/browser_client_test.go`

**Interfaces:**
- Produces `BrowserClient` with startup/shutdown, health wait, authenticated `Call`, and worker restart support.
- `Call(ctx, method string, params any, result any) error` sends JSON-RPC over localhost with the bearer token.
- Worker startup returns the selected `port: 0` address to the client.
- Separate startup, navigation, action, and snapshot timeouts are supported.

- [ ] **Step 1: Write failing tests**

Use an in-process HTTP test worker to assert bearer authentication, request IDs, success/error decoding, context timeout, health polling, and restart after a simulated worker exit. Assert no token is included in returned errors.

- [ ] **Step 2: Run focused tests**

Run: `go test ./tools -run 'TestBrowserClient' -v`
Expected: FAIL because `BrowserClient` does not exist.

- [ ] **Step 3: Implement the client**

Use `net/http`, `os/exec`, `context`, and JSON encoding. Start the Node worker with the configured script, read its selected local endpoint/token through a controlled startup channel or environment/IPC mechanism, poll `/health`, and expose authenticated RPC calls. On process exit, clear the ready state so callers receive an explicit unavailable error until restart completes.

- [ ] **Step 4: Run focused tests**

Run: `go test ./tools -run 'TestBrowserClient' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tools/browser_client.go tools/browser_client_test.go
git commit -m "feat: add Go Playwright worker client"
```

---

### Task 5: Expose V1 browser tools through `tools.Registry`

**Files:**
- Create: `tools/browser_tools.go`
- Create: `tools/browser_tools_test.go`
- Modify: `tools/registry.go`
- Modify: `tools/registry_test.go`

**Interfaces:**
- Browser handlers call `BrowserClient` and use the current `session_id` supplied by each tool invocation.
- Tool names and core inputs are exactly: `browser_open`, `browser_close`, `browser_navigate`, `browser_snapshot`, `browser_click`, `browser_fill`, `browser_press`, `browser_select`, `browser_scroll`, `browser_get_text`, `browser_screenshot`.
- `web_fetch` is registered alongside the existing nine tools.

- [ ] **Step 1: Write failing registry/tool tests**

Update the registry test to expect 21 definitions total and assert all existing names plus `web_fetch` and the 11 browser names. Add handler tests using a fake browser client to verify JSON arguments map to the correct RPC method and errors become `ToolResult{IsError:true}` while preserving the call ID.

- [ ] **Step 2: Run focused tests**

Run: `go test ./tools -run 'TestRegistry|TestBrowserTools' -v`
Expected: FAIL because the new definitions/handlers are absent.

- [ ] **Step 3: Implement registrations and handlers**

Keep the existing `sdk.ToolExecutor` interface unchanged. Add dependency injection to the registry constructor or a focused constructor/factory so the registry can own the `BrowserClient` and `web_fetch` dependencies without adding browser logic to `sdk.Agent`. Keep schemas explicit and minimal.

- [ ] **Step 4: Run focused tests**

Run: `go test ./tools -run 'TestRegistry|TestBrowserTools' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tools/registry.go tools/registry_test.go tools/browser_tools.go tools/browser_tools_test.go
git commit -m "feat: register web and browser agent tools"
```

---

### Task 6: Add runtime configuration and lifecycle integration

**Files:**
- Create: `runtime/browser_config.go`
- Test: `runtime/browser_config_test.go`
- Modify: `runtime/runtime.go`
- Modify: `cmd/ai/main.go`
- Modify: `.config/transport.example.env`
- Modify: `.gitignore` if generated browser state requires it

**Interfaces:**
- `runtime.BrowserConfig` contains enabled state, worker host/port, headless mode, idle timeout, private-network flag, and navigation/action/snapshot limits matching the approved design.
- Existing provider JSON configuration remains unchanged.
- Existing env-based application startup remains the source of runtime overrides; do not add a YAML parser just for browser settings.
- `cmd/ai` starts the browser worker before constructing the Agent registry and closes it on context shutdown.

- [ ] **Step 1: Write failing config/lifecycle tests**

Test default values, explicit environment parsing, invalid durations/ports, disabled browser behavior, and that the runtime can construct an Agent with browser tools disabled without starting Node.

- [ ] **Step 2: Run focused tests**

Run: `go test ./runtime ./cmd/ai -run 'TestBrowser' -v`
Expected: FAIL because browser configuration/lifecycle integration is absent.

- [ ] **Step 3: Implement configuration and startup**

Map the design values to environment variables such as `AI_BROWSER_ENABLED`, `AI_BROWSER_HEADLESS`, `AI_BROWSER_HOST`, `AI_BROWSER_PORT`, `AI_BROWSER_IDLE_TIMEOUT`, `AI_BROWSER_ALLOW_PRIVATE`, `AI_BROWSER_NAVIGATION_TIMEOUT`, `AI_BROWSER_ACTION_TIMEOUT`, and `AI_BROWSER_SNAPSHOT_TIMEOUT`. Preserve safe defaults: enabled only when explicitly configured if that matches current runtime behavior; private access remains false. Start the worker only when browser tools are enabled and pass the resulting client into the registry.

- [ ] **Step 4: Run focused tests**

Run: `go test ./runtime ./cmd/ai -run 'TestBrowser' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/browser_config.go runtime/browser_config_test.go runtime/runtime.go cmd/ai/main.go .config/transport.example.env .gitignore
git commit -m "feat: integrate browser runtime configuration"
```

---

### Task 7: Add setup script and browser CI

**Files:**
- Create: `scripts/setup-browser.sh`
- Create: `.github/workflows/browser.yml`

**Interfaces:**
- Setup script is executable, root-compatible, and never invokes `sudo`.
- Script verifies Node.js/npm, runs `npm ci` in `browser/`, runs `npx playwright install --with-deps chromium`, and performs a basic Chromium launch/health check.
- CI runs Go tests/vet in the existing workflow and browser tests in a separate job with Chromium installed.

- [ ] **Step 1: Write shell/CI checks**

Test the script with a mocked `node`, `npm`, and Playwright command path where possible, and validate the workflow YAML structure with the repository's normal GitHub Actions parsing. The integration job must exercise `npm ci`, Chromium installation, and `npm test`.

- [ ] **Step 2: Run local checks and verify failure before implementation**

Run: `bash -n scripts/setup-browser.sh` and `cd browser && npm test`.
Expected: the script check fails until the script exists; worker tests are covered by Task 3 once dependencies are installed.

- [ ] **Step 3: Implement setup and CI**

Use Node/npm detection, fail clearly when Node is missing rather than silently installing an unrelated Node version, install dependencies with the lockfile, install only Chromium with system dependencies, and run a real headless launch check. Keep browser CI separate from `.github/workflows/go.yml`.

- [ ] **Step 4: Verify**

Run: `bash -n scripts/setup-browser.sh`; then `./scripts/setup-browser.sh` as root on the target Linux environment; then `cd browser && npm test`.
Expected: syntax check, Chromium setup, and browser tests all PASS.

- [ ] **Step 5: Commit**

```bash
git add scripts/setup-browser.sh .github/workflows/browser.yml
chmod +x scripts/setup-browser.sh
git add scripts/setup-browser.sh
git commit -m "ci: add Playwright browser setup and tests"
```

---

### Task 8: Update documentation and security-facing behavior

**Files:**
- Modify: `README.md`
- Modify: `.config/transport.example.env`
- Modify: `transport/discord/adapter.go` only if browser trace output needs explicit redaction
- Test: `transport/discord/adapter_test.go` if trace formatting changes

**Interfaces:**
- Documentation explains when AI should choose `web_fetch` versus browser tools, how to install Chromium, the environment variables, session isolation, SSRF defaults, and V1 exclusions.
- Discord trace output never prints bearer tokens, cookies, raw HTML, or internal RPC payloads.

- [ ] **Step 1: Add a regression test for sensitive trace data**

If browser trace payloads are surfaced, construct a trace containing a fake bearer token/cookie/raw RPC field and assert the Discord formatter omits or redacts those fields. If browser traces use only the existing tool call/result content, verify the existing formatter remains safe and make no unnecessary transport change.

- [ ] **Step 2: Run the focused test**

Run: `go test ./transport/discord -run 'Test.*Trace' -v`
Expected: PASS or fail only if the new regression test exposes an actual leak.

- [ ] **Step 3: Update README and example env**

Document the commands `./scripts/setup-browser.sh`, the browser environment variables, the `web_fetch`/browser selection rule, the session/context model, security defaults, and unsupported V1 capabilities. Do not document credentials or suggest using a normal personal Chrome profile.

- [ ] **Step 4: Run repository tests**

Run: `go test ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add README.md .config/transport.example.env transport/discord/adapter.go transport/discord/adapter_test.go
git commit -m "docs: document browser and web tools"
```

---

### Task 9: Full verification and integration proof

**Files:**
- Modify only files required by verified failures from Tasks 1–8.
- Test: all Go and Node tests plus CI workflows.

**Interfaces:**
- Final repository exposes all 21 tools through `tools.Registry` while preserving the existing Agent loop and session persistence behavior.
- Browser worker restart returns an explicit unavailable/session-loss condition instead of pretending old browser state survived.

- [ ] **Step 1: Run all Go tests**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 2: Run Go vet**

Run: `go vet ./...`
Expected: PASS.

- [ ] **Step 3: Run browser tests**

Run: `cd browser && npm test`
Expected: PASS.

- [ ] **Step 4: Run a real local integration flow**

Start the worker, then exercise: `browser_open → browser_navigate → browser_snapshot → browser_fill/click/press → browser_snapshot → browser_get_text`, plus a `web_fetch` call against a controlled HTTP endpoint. Verify two session IDs cannot see each other's cookies/pages and verify private-network navigation is rejected.

- [ ] **Step 5: Inspect the final diff**

Run: `git status --short`, `git diff HEAD~1 --stat`, and inspect every modified security/config file for accidental credentials, broad network access, or unsupported V1 capabilities.

- [ ] **Step 6: Commit only verified fixes**

```bash
git add <only-files-changed-by-verified-fixes>
git commit -m "fix: resolve browser integration verification findings"
```

- [ ] **Step 7: Push/Actions verification loop**

After each implementation commit, wait about 15 seconds and inspect the latest GitHub Actions run for that commit. If it is still running, wait another 15 seconds and check again. If it fails, inspect the failing job logs, make the smallest targeted fix, commit, and repeat. Do not declare completion until the required workflows are green.
