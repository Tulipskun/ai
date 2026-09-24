# Tulipskun/ai

The AI harness behind **AIxodia** (the Android client). `ai` is a single
daemon whose only gateway is a WebSocket published through a Cloudflare quick
tunnel; runtime configuration and session state live in Cloudflare D1 (REQ-047).

## Run

```bash
go build -o ai ./cmd/ai
./ai          # or: ./ai daemon
```

There is no CLI, no Discord bot and no self-update any more (CHANGE-059): the
phone is the only client, the daemon owns no state, and there is nothing to
hand over on restart.

## Gateway and state

- **One ingress**: `transport/mobile` listens on localhost and publishes itself
  with `cloudflared tunnel --url …`; the phone discovers the URL through
  `GET /api/node` on the Worker.
- **Two-step access** (REQ-046): the unguessable tunnel hostname, then
  `Authorization: Bearer <D1 token>` on the WebSocket handshake, verified
  against the Worker before the socket is upgraded. Missing header → 401; five
  wrong tokens → 429 and a progressive lockout (30 → 60 → 120 → 240 → 300s);
  Worker unreachable → 503, fail closed.
- **Stateless** (CON-012): `runtime/d1store` pulls `config:*` and
  `sessions/<id>` from D1 into the state root after the first verified
  connection and pushes changes back. Local files stay a cache, so wiping
  `~/.local/share/ai` is recoverable.
- **No credential of its own**: the D1 token a phone presents is the only
  secret, and it lives in memory until the process restarts.

## config/entry.json

```json
{
  "mobile": {
    "enabled": true,
    "worker_base": "https://aixodia.<subdomain>.workers.dev",
    "listen": "127.0.0.1:18789",
    "tunnel": true,
    "cloudflared": "cloudflared",
    "sync_config": true,
    "sync_sessions": true
  }
}
```

Provider keys, the system prompt and session history are **not** configured
here — they come from D1 (`/api/state/config:provider`, `/api/state/config:system`,
`/api/state/sessions/<id>`). The token is never written to a file.

## CI

`.github/workflows/go.yml` builds, tests and vets on every push;
`release.yml` produces the Linux arm64 binary as an artifact (no release tags).
