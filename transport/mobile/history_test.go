package mobile

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeHistory struct {
	sessions map[string]SessionRow
	turns    []TurnRow
	node     NodeRow
	nodeSeen bool
	lastSeq  int64
}

func newFakeHistory() *fakeHistory {
	return &fakeHistory{sessions: map[string]SessionRow{}}
}

func (f *fakeHistory) ListSessions(context.Context, int) ([]SessionRow, error) {
	out := make([]SessionRow, 0, len(f.sessions))
	for _, row := range f.sessions {
		out = append(out, row)
	}
	return out, nil
}

func (f *fakeHistory) CreateSession(_ context.Context, id, title, model string) (SessionRow, error) {
	row, ok := f.sessions[id]
	if !ok {
		row = SessionRow{ID: id, Title: title, Model: model}
		f.sessions[id] = row
	}
	return row, nil
}

func (f *fakeHistory) RenameSession(_ context.Context, id, title string) (SessionRow, bool, error) {
	row, ok := f.sessions[id]
	if !ok {
		return SessionRow{}, false, nil
	}
	row.Title = title
	f.sessions[id] = row
	return row, true, nil
}

func (f *fakeHistory) DeleteSession(_ context.Context, id string) (bool, error) {
	_, ok := f.sessions[id]
	delete(f.sessions, id)
	kept := f.turns[:0]
	for _, turn := range f.turns {
		if !strings.HasPrefix(turn.Text, id+":") {
			kept = append(kept, turn)
		}
	}
	f.turns = kept
	return ok, nil
}

func (f *fakeHistory) Turns(_ context.Context, sessionID string, beforeSeq int64, limit int) ([]TurnRow, error) {
	out := []TurnRow{}
	for _, turn := range f.turns {
		if beforeSeq > 0 && turn.Seq >= beforeSeq {
			continue
		}
		if strings.HasPrefix(turn.Text, sessionID+":") {
			out = append(out, turn)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeHistory) AppendTurn(_ context.Context, sessionID, role, agent, jobID, text string) (int64, error) {
	f.lastSeq++
	f.turns = append(f.turns, TurnRow{Seq: f.lastSeq, Role: role, Agent: agent, JobID: jobID, Text: sessionID + ":" + text})
	return f.lastSeq, nil
}

func (f *fakeHistory) Node(context.Context) (NodeRow, bool, error) { return f.node, f.nodeSeen, nil }

type allowVerifier struct{ reject bool }

func (v allowVerifier) VerifyToken(context.Context, string) error {
	if v.reject {
		return ErrTokenRejected
	}
	return nil
}

func historyRequest(t *testing.T, handler http.Handler, method, path, token string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestHistoryServesThePhoneContract(t *testing.T) {
	store := newFakeHistory()
	cache := &ramCache{}
	store.sessions["work-1"] = SessionRow{ID: "work-1", Title: "แชททดสอบ", Model: "m", UpdatedAt: 5}
	store.node = NodeRow{TunnelURL: "https://quiet-fog.trycloudflare.com", Version: "one-url", Heartbeat: 1 << 40}
	store.nodeSeen = true
	gate := NewGate(GateConfig{Verify: allowVerifier{}, Cache: cache})
	handler := NewHistoryHandler(store, gate)

	rec := historyRequest(t, handler, http.MethodGet, "/api/sessions", "cf-token", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/sessions = %d: %s", rec.Code, rec.Body)
	}
	var rows []SessionRow
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(rows) != 1 || rows[0].Title != "แชททดสอบ" {
		t.Fatalf("sessions = %+v", rows)
	}

	rec = historyRequest(t, handler, http.MethodGet, "/api/sessions/work-1/turns?before_seq=0&limit=50", "cf-token", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET turns = %d", rec.Code)
	}
	var page struct {
		Turns []TurnRow `json:"turns"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode turns: %v", err)
	}
	if page.Turns == nil {
		t.Fatal("turns must serialise as [] rather than null")
	}

	rec = historyRequest(t, handler, http.MethodPost, "/api/sessions", "cf-token", `{"id":"work-2","title":"ใหม่"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/sessions = %d: %s", rec.Code, rec.Body)
	}
	rec = historyRequest(t, handler, http.MethodPatch, "/api/sessions/work-2", "cf-token", `{"title":"เปลี่ยนชื่อ"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d: %s", rec.Code, rec.Body)
	}
	rec = historyRequest(t, handler, http.MethodDelete, "/api/sessions/work-2", "cf-token", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE = %d: %s", rec.Code, rec.Body)
	}
	rec = historyRequest(t, handler, http.MethodDelete, "/api/sessions/missing", "cf-token", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE missing = %d, want 404", rec.Code)
	}

	rec = historyRequest(t, handler, http.MethodGet, "/api/node", "cf-token", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/node = %d", rec.Code)
	}
	var node struct {
		TunnelURL string `json:"tunnel_url"`
		Online    bool   `json:"online"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &node); err != nil {
		t.Fatalf("decode node: %v", err)
	}
	if node.TunnelURL != store.node.TunnelURL || node.Online {
		t.Fatalf("node = %+v, want the announced URL and an old heartbeat", node)
	}
}

func TestHistoryRefusesRequestsWithoutAUsableToken(t *testing.T) {
	store := newFakeHistory()
	cache := &ramCache{}
	handler := NewHistoryHandler(store, NewGate(GateConfig{Verify: allowVerifier{}, Cache: cache}))

	if rec := historyRequest(t, handler, http.MethodGet, "/api/sessions", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no header = %d, want 401", rec.Code)
	}
	rejecting := NewHistoryHandler(store, NewGate(GateConfig{Verify: allowVerifier{reject: true}, Cache: cache}))
	if rec := historyRequest(t, rejecting, http.MethodGet, "/api/sessions", "wrong", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d, want 401", rec.Code)
	}
	if cache.Get() != "" {
		t.Fatal("a rejected token must never be cached")
	}
}

func TestHistoryKeepsTheStateTableOutOfReach(t *testing.T) {
	store := newFakeHistory()
	cache := &ramCache{}
	cache.Adopt("cf-token")
	handler := NewHistoryHandler(store, NewGate(GateConfig{Verify: allowVerifier{}, Cache: cache}))
	for _, path := range []string{"/api/state", "/api/state/config:provider", "/api/devices"} {
		if rec := historyRequest(t, handler, http.MethodGet, path, "cf-token", ""); rec.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404 (provider keys must stay unreachable from the tunnel)", path, rec.Code)
		}
	}
}

func TestHistoryAdoptsTheVerifiedTokenForTheDaemon(t *testing.T) {
	store := newFakeHistory()
	cache := &ramCache{}
	handler := NewHistoryHandler(store, NewGate(GateConfig{Verify: allowVerifier{}, Cache: cache}))
	if rec := historyRequest(t, handler, http.MethodGet, "/api/sessions", "fresh-token", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET = %d", rec.Code)
	}
	if cache.Get() != "fresh-token" {
		t.Fatalf("cached token = %q, want the one just verified", cache.Get())
	}
}
