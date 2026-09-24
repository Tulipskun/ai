package mobile

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHistoryProxyForwardsAllowedPathsWithToken(t *testing.T) {
	var seenPath, seenAuth, seenMethod string
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath, seenAuth, seenMethod = r.URL.Path, r.Header.Get("Authorization"), r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"turns":[]}`))
	}))
	defer worker.Close()

	tokens := &stubTokens{}
	proxy := NewHistoryProxy(worker.URL, tokens)
	front := httptest.NewServer(proxy)
	defer front.Close()

	req, _ := http.NewRequest(http.MethodGet, front.URL+"/api/sessions/abc/turns?limit=50", nil)
	req.Header.Set("Authorization", "Bearer d1-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "turns") {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if seenPath != "/api/sessions/abc/turns" || seenMethod != http.MethodGet {
		t.Fatalf("forwarded %s %s", seenMethod, seenPath)
	}
	if seenAuth != "Bearer d1-token" {
		t.Fatalf("Authorization not forwarded: %q", seenAuth)
	}
}

func TestHistoryProxyRequiresAuthorization(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("worker must not be called without a token")
	}))
	defer worker.Close()
	front := httptest.NewServer(NewHistoryProxy(worker.URL, &stubTokens{}))
	defer front.Close()

	resp, err := http.Get(front.URL + "/api/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestHistoryProxyRefusesStateAndOtherPaths(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /api/state holds provider API keys: it must never be reachable
		// through the tunnel even with a valid token.
		t.Fatalf("must not proxy %s", r.URL.Path)
	}))
	defer worker.Close()
	front := httptest.NewServer(NewHistoryProxy(worker.URL, &stubTokens{}))
	defer front.Close()

	for _, path := range []string{"/api/state/config:provider", "/api/devices", "/", "/api/sessions/abc/jobs"} {
		req, _ := http.NewRequest(http.MethodGet, front.URL+path, nil)
		req.Header.Set("Authorization", "Bearer d1-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusNotImplemented {
			t.Fatalf("%s: status = %d, want 404", path, resp.StatusCode)
		}
	}
}
