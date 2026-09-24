package d1store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeWorker implements the subset of the AIxodia Worker contract the client
// uses: /api/ping, /api/state (GET/PUT) and /api/state?prefix=.
type fakeWorker struct {
	mu     sync.Mutex
	state  map[string]string
	token  string
	calls  int
	prefix string
}

func newFakeWorker(token string) *fakeWorker {
	return &fakeWorker{state: map[string]string{}, token: token}
}

func (f *fakeWorker) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls++
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+f.token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+f.token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.prefix = r.URL.Query().Get("prefix")
		keys := []string{}
		for k := range f.state {
			if strings.HasPrefix(k, f.prefix) {
				keys = append(keys, k)
			}
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	})
	mux.HandleFunc("/api/state/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+f.token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/api/state/")
		if r.Method == http.MethodGet {
			value, ok := f.state[key]
			if !ok {
				_ = json.NewEncoder(w).Encode(map[string]any{"key": key, "value": nil})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"key": key, "value": value})
			return
		}
		var body struct {
			Value string `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.state[key] = body.Value
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func TestVerifyTokenDistinguishesWrongFromDown(t *testing.T) {
	worker := newFakeWorker("d1-token")
	server := httptest.NewServer(worker.handler())
	defer server.Close()

	tokens := NewMemoryToken()
	client := NewClient(server.URL, tokens.Get)
	if err := client.VerifyToken(context.Background(), "d1-token"); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if err := client.VerifyToken(context.Background(), "nope"); !strings.Contains(err.Error(), ErrTokenRejected.Error()) {
		t.Fatalf("wrong token error = %v, want ErrTokenRejected", err)
	}

	server.Close()
	if err := client.VerifyToken(context.Background(), "d1-token"); err == nil {
		t.Fatal("expected a transport error when the Worker is down")
	} else if strings.Contains(err.Error(), ErrTokenRejected.Error()) {
		t.Fatalf("an outage must not look like a rejected token: %v", err)
	}
}

func TestCallsWithoutTokenFailWithErrNoToken(t *testing.T) {
	worker := newFakeWorker("d1-token")
	server := httptest.NewServer(worker.handler())
	defer server.Close()

	tokens := NewMemoryToken()
	client := NewClient(server.URL, tokens.Get)
	if _, _, err := client.Get(context.Background(), "config:provider"); err != ErrNoToken {
		t.Fatalf("Get without token = %v, want ErrNoToken", err)
	}
	if err := client.Put(context.Background(), "config:provider", "{}"); err != ErrNoToken {
		t.Fatalf("Put without token = %v, want ErrNoToken", err)
	}
}

func TestMemoryTokenAdoptIsVolatile(t *testing.T) {
	tokens := NewMemoryToken()
	if tokens.Get() != "" {
		t.Fatal("a fresh daemon must hold no credential")
	}
	tokens.Adopt("d1-token")
	if tokens.Get() != "d1-token" {
		t.Fatalf("token = %q", tokens.Get())
	}
	tokens.Adopt("   ")
	if tokens.Get() != "d1-token" {
		t.Fatal("a blank adopt must not wipe the token")
	}
	tokens.Clear()
	if tokens.Get() != "" {
		t.Fatal("Clear did not empty the token")
	}
}

func TestHydrateAndPushConfig(t *testing.T) {
	worker := newFakeWorker("d1-token")
	server := httptest.NewServer(worker.handler())
	defer server.Close()

	tokens := NewMemoryToken()
	tokens.Adopt("d1-token")
	client := NewClient(server.URL, tokens.Get)

	stateRoot := t.TempDir()
	files := DefaultConfigFiles(stateRoot)

	// Push local files up.
	if err := os.MkdirAll(filepath.Join(stateRoot, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	providerJSON := `{"providers":[{"name":"demo","adapter":"openai","http_endpoint":"https://example.invalid","api_keys":["k"]}]}`
	if err := os.WriteFile(files[0].Path, []byte(providerJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := client.PushConfig(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.PushedConfig) != 1 || report.PushedConfig[0] != "config:provider" {
		t.Fatalf("pushed = %v", report.PushedConfig)
	}

	// A fresh daemon state root hydrates the same content back.
	fresh := t.TempDir()
	report, err = client.HydrateConfig(context.Background(), DefaultConfigFiles(fresh))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.PulledConfig) != 1 {
		t.Fatalf("pulled = %v", report.PulledConfig)
	}
	got, err := os.ReadFile(filepath.Join(fresh, "config", "provider.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != providerJSON {
		t.Fatalf("hydrated config = %q", got)
	}
	info, err := os.Stat(filepath.Join(fresh, "config", "provider.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("hydrated config mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestHydrateKeepsLocalFileWhenD1HasNoCopy(t *testing.T) {
	worker := newFakeWorker("d1-token")
	server := httptest.NewServer(worker.handler())
	defer server.Close()

	tokens := NewMemoryToken()
	tokens.Adopt("d1-token")
	client := NewClient(server.URL, tokens.Get)

	stateRoot := t.TempDir()
	files := DefaultConfigFiles(stateRoot)
	if err := os.MkdirAll(filepath.Join(stateRoot, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(files[0].Path, []byte(`{"local":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := client.HydrateConfig(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.PulledConfig) != 0 {
		t.Fatalf("pulled = %v, want none", report.PulledConfig)
	}
	got, err := os.ReadFile(files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"local":true}` {
		t.Fatalf("local config was modified: %q", got)
	}
}

func TestSessionRoundTrip(t *testing.T) {
	worker := newFakeWorker("d1-token")
	server := httptest.NewServer(worker.handler())
	defer server.Close()

	tokens := NewMemoryToken()
	tokens.Adopt("d1-token")
	client := NewClient(server.URL, tokens.Get)

	source := t.TempDir()
	sessionDir := filepath.Join(source, "sessions")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// CON-002 keeps one SQLite file per session; the test uses a byte-identical
	// stand-in because the sync layer only moves bytes.
	dbBytes := []byte("SQLite format 3\x00pretend-session")
	name := "c2Vzc2lvbi1vbmU.db"
	if err := os.WriteFile(filepath.Join(sessionDir, name), dbBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := client.PushSession(context.Background(), "session-one", filepath.Join(sessionDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.PushedSession) != 1 {
		t.Fatalf("pushed sessions = %v", report.PushedSession)
	}

	// A brand-new state root restores the file with the SDK's own naming.
	fresh := t.TempDir()
	encode := base64Name
	report, err = client.HydrateSessions(context.Background(), filepath.Join(fresh, "sessions"), encode)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.PulledSession) != 1 || report.PulledSession[0] != "session-one" {
		t.Fatalf("pulled sessions = %v", report.PulledSession)
	}
	got, err := os.ReadFile(filepath.Join(fresh, "sessions", encode("session-one")))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(dbBytes) {
		t.Fatalf("restored bytes = %q", got)
	}
}

func TestOversizedValueIsSkippedNotTruncated(t *testing.T) {
	worker := newFakeWorker("d1-token")
	server := httptest.NewServer(worker.handler())
	defer server.Close()

	tokens := NewMemoryToken()
	tokens.Adopt("d1-token")
	client := NewClient(server.URL, tokens.Get)

	stateRoot := t.TempDir()
	files := DefaultConfigFiles(stateRoot)
	if err := os.MkdirAll(filepath.Join(stateRoot, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	huge := make([]byte, maxValueBytes+10)
	for i := range huge {
		huge[i] = 'x'
	}
	if err := os.WriteFile(files[0].Path, huge, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := client.PushConfig(context.Background(), files)
	if err != nil {
		t.Fatalf("an oversized file must not fail the whole pass: %v", err)
	}
	if len(report.SkippedOversize) != 1 {
		t.Fatalf("skipped = %v, want the oversized key", report.SkippedOversize)
	}
	if len(report.PushedConfig) != 0 {
		t.Fatalf("pushed = %v, want none", report.PushedConfig)
	}

	// Direct Put refuses instead of letting the Worker reject it mid-turn.
	if err := client.Put(context.Background(), "config:provider", string(huge)); err == nil {
		t.Fatal("expected Put to refuse an oversized value")
	}
}

// base64Name mirrors sdk.SessionDBPath so the test asserts the real naming.
func base64Name(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id)) + ".db"
}
