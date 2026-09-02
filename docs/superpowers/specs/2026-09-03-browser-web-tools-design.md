# Browser and Web Tools Design

- Date: 2026-09-03
- Repository: `Tulipskun/ai`
- Status: Design approved; implementation not started

## 1. Goal

Add two capabilities to the Go Agent:

1. `web_fetch` for fast HTTP/HTTPS content retrieval.
2. `browser_*` tools for interactive browser automation through Playwright/Chromium.

The AI sees native SDK tools and does not need to know that browser operations are implemented by a separate Node.js worker.

## 2. Architecture

```text
Go Agent
 ├── web_fetch ───────────────> Go net/http ─────> Web
 │
 └── browser_* ──────────────> HTTP JSON-RPC
                                  │
                                  v
                           Node Browser Worker
                                  │
                                  v
                              Playwright
                                  │
                                  v
                               Chromium
```

`web_fetch` remains in Go because it is lightweight. Browser automation is isolated in a Node.js worker because Playwright is a Node-native browser automation library. Playwright supports Chromium and requires the matching browser binaries to be installed explicitly; Chromium-only installation is preferred for v1. citeturn0search0turn0search2

The worker binds only to `127.0.0.1`. Go starts it, waits for `/health`, and registers browser tools only after the worker is ready. A worker crash triggers restart; browser session state is not guaranteed to survive a crash.

## 3. RPC Protocol

Endpoints:

- `GET /health`
- `POST /rpc`

Request:

```json
{
  "id": "req-123",
  "method": "browser.navigate",
  "params": {
    "session_id": "discord:channel:123",
    "url": "https://example.com"
  }
}
```

Success:

```json
{"id":"req-123","ok":true,"result":{}}
```

Failure:

```json
{"id":"req-123","ok":false,"error":{"code":"...","message":"..."}}
```

The worker generates a random authentication token at startup. Go sends it as `Authorization: Bearer <token>`. Navigation, action, snapshot, and worker-startup timeouts are independently configurable.

## 4. Browser Sessions

One AI `Session` maps to one isolated Playwright `BrowserContext`. A context can contain multiple pages/tabs.

```text
Session A -> Context A -> Page 1, Page 2
Session B -> Context B -> Page 1
```

A single Chromium browser instance is shared by contexts rather than launching a Chromium process for every AI session. Context isolation keeps cookies, localStorage, and session state separate.

`browser_open` creates a context/page when absent and creates another page when the context already exists. `browser_navigate` operates on the current page.

Default browser contexts are ephemeral. Persistent profiles are optional future functionality and, if enabled later, must use a dedicated automation profile rather than the user's normal Chrome profile. citeturn0search5

An idle timeout closes inactive pages/contexts.

## 5. Snapshot and Element References

`browser_snapshot` is the primary browser observation mechanism. It returns a normalized accessibility-oriented representation of the current page with stable-for-that-snapshot element references:

```text
URL: https://example.com
TITLE: Example Domain

[heading]
Example Domain

[link]
More information
ref=e1
```

Actions operate on references instead of guessed screen coordinates. References are scoped to the snapshot. If the DOM changes and a reference is invalid, the tool returns an error and the AI must obtain a new snapshot.

`screenshot` is a secondary visual tool for cases where accessibility/text information is insufficient. It is not the primary control mechanism.

## 6. V1 Tools

### Web

- `web_fetch(url)`

The result contains status, final URL, content type, title where available, extracted text, and a truncation indicator. Raw HTML is not returned by default.

### Browser

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

Each browser tool receives the AI session identifier. Element-action tools receive a snapshot reference where applicable.

V1 explicitly excludes arbitrary JavaScript/eval, raw CDP, arbitrary filesystem access, `file://` navigation, cookie export/import, and arbitrary downloads/uploads.

## 7. Web Fetch

`web_fetch` uses Go HTTP facilities and is intended for static pages, documentation, and APIs. Browser automation is reserved for JavaScript-heavy pages, interaction, login, and workflows that require page actions.

`web_fetch` enforces:

- HTTP/HTTPS only.
- Request timeout.
- Maximum response size.
- Maximum extracted-text size.
- Maximum redirect count.
- URL validation on the initial URL and every redirect.

## 8. Network Security / SSRF

Both `web_fetch` and `browser_navigate` use the same URL/network policy.

By default, access to loopback, RFC1918/private ranges, link-local ranges, IPv6 local/private ranges, and cloud metadata-style addresses is blocked. Validation must operate on resolved destination IPs rather than only URL strings, with protection against DNS rebinding. Redirect destinations are revalidated.

Default configuration:

```yaml
browser:
  network:
    allow_private: false
```

An explicit private-network allowlist may be added later. AI-provided webpage content is untrusted data and must never be treated as system/tool instructions.

## 9. Tool Integration

The existing `tools.Registry`/`sdk.ToolExecutor` model is extended with web/browser registrations. The Agent already obtains `Definitions()` and executes returned `ToolCall`s through the executor, so browser implementation should remain behind the tool executor boundary rather than adding browser-specific logic to the Agent loop.

Trace events should record browser tool calls/results using the existing trace mechanism. User-facing Discord output may show tool activity and AI response content, but must not expose bearer tokens, cookies, raw HTML, internal RPC payloads, or other sensitive worker state.

## 10. Configuration

Initial configuration shape:

```yaml
browser:
  enabled: true
  worker:
    host: 127.0.0.1
    port: 0
  chromium:
    headless: true
  session:
    idle_timeout: 30m
  network:
    allow_private: false
  limits:
    navigation_timeout: 30s
    action_timeout: 10s
    snapshot_timeout: 10s
```

`port: 0` lets the OS select an available local port. The worker communicates the selected port to Go during startup.

## 11. Setup

The browser worker is kept separate from the Go module, for example:

```text
browser/
  package.json
  package-lock.json
  server.mjs
  playwright.mjs
scripts/
  setup-browser.sh
```

The setup script is intended to run as root without `sudo` and should:

1. Verify Node.js and npm.
2. Install Node dependencies with `npm ci`.
3. Install only Chromium and its required OS dependencies.
4. Launch a basic Chromium health check.
5. Report readiness.

Playwright documents `npx playwright install --with-deps chromium` for installing Chromium together with system dependencies. citeturn0search0turn0search3

## 12. Testing and CI

Go tests remain covered by the existing `go test ./...` and `go vet ./...` workflow. Browser worker tests should separately cover:

- RPC request/response handling.
- Worker health/lifecycle.
- Session/context/page isolation.
- Snapshot references and stale-reference errors.
- Navigation/action timeouts.
- SSRF/private-network blocking.
- Redirect revalidation.
- Basic Chromium navigation integration.

Browser integration can be a separate CI job so normal Go CI remains lightweight. Playwright browser binaries should be installed explicitly in the browser CI job. citeturn0search0turn0search4

## 13. Error Handling

Tool failures return normal `sdk.ToolResult` errors so the Agent can observe the failure and decide whether to retry, snapshot again, or use another tool.

Important cases include:

- Worker unavailable.
- Worker RPC timeout.
- Navigation timeout.
- Invalid URL.
- SSRF/network policy denial.
- Stale browser reference.
- Missing session/page.
- Browser crash.
- Response/text truncation.

Browser-worker restart does not silently claim that previous browser state still exists.

## 14. Data Flow Example

For a request such as "open a website, search for X, and tell me the result":

```text
AI
 -> browser_open
 -> browser_navigate
 -> browser_snapshot
 -> browser_fill(ref, "X")
 -> browser_press(ref, "Enter")
 -> browser_snapshot
 -> browser_get_text(ref)
 -> final response
```

For a static documentation request:

```text
AI
 -> web_fetch(url)
 -> final response
```

The model chooses the lighter fetch path when interaction is unnecessary.

## 15. Scope Boundary

This design intentionally establishes the minimum useful browser capability first. Authentication persistence, downloads/uploads, arbitrary JavaScript execution, raw CDP access, and advanced browser profiles can be designed separately after the v1 tool path is stable.
