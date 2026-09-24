package mobile

import (
	"io"
	"net/http"
	"strings"
	"time"
)

// allowlist keeps the tunnel surface minimal: exactly the endpoints the app
// needs, so the public quick-tunnel URL cannot be turned into a general-purpose
// proxy for the Worker (which would also expose /api/state and provider keys).
var allowedHistoryPaths = map[string]bool{
	"/api/sessions": true, // list / create sessions
	"/api/node":     true, // tunnel discovery
	"/api/ping":     true, // token check
}

// allowedTurns matches /api/sessions/<id>/turns.
func allowedTurns(path string) bool {
	rest := strings.TrimPrefix(path, "/api/sessions/")
	if rest == path {
		return false
	}
	parts := strings.Split(rest, "/")
	return len(parts) == 2 && parts[1] == "turns" && parts[0] != ""
}

// NewHistoryProxy lets the phone use ONE address for everything: the quick
// tunnel. Requests carry the same D1 token in the Authorization header as the
// WebSocket handshake, and the daemon forwards them to the Worker with the
// token it already holds — the phone never needs a second URL, and the daemon
// never stores a credential of its own (REQ-046(3), CON-012).
func NewHistoryProxy(workerBase string, tokens TokenStore) http.Handler {
	base := strings.TrimRight(workerBase, "/")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if base == "" {
			http.Error(w, "history proxy is not configured", http.StatusNotImplemented)
			return
		}
		if r.Header.Get("Authorization") == "" {
			// Same rule as the handshake: no token, no access.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"missing Authorization header"}`))
			return
		}
		path := r.URL.Path
		if !allowedHistoryPaths[path] && !allowedTurns(path) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not proxied"}`))
			return
		}
		forward, err := http.NewRequestWithContext(r.Context(), r.Method, base+path, r.Body)
		if err != nil {
			http.Error(w, "proxy: bad request", http.StatusBadGateway)
			return
		}
		for _, header := range []string{"Authorization", "Content-Type"} {
			if value := r.Header.Get(header); value != "" {
				forward.Header.Set(header, value)
			}
		}
		forward.Header.Set("User-Agent", "ai")
		if q := r.URL.RawQuery; q != "" {
			forward.URL.RawQuery = q
		}
		resp, err := proxyClient.Do(forward)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"worker unreachable"}`))
			return
		}
		defer resp.Body.Close()
		for _, header := range []string{"Content-Type", "Retry-After"} {
			if value := resp.Header.Get(header); value != "" {
				w.Header().Set(header, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
}

var proxyClient = &http.Client{Timeout: 60 * time.Second}
