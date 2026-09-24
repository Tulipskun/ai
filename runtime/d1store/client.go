// Package d1store syncs the daemon's runtime state with Cloudflare D1 through
// the AIxodia Worker (REQ-046, CON-012).
//
// D1 is the authoritative copy; the local files stay exactly what the rest of
// the runtime already reads and writes (config/*.json and one SQLite file per
// session under data/sessions/). Hydrate pulls the cloud copy down, Push sends
// local changes up. A daemon that loses its whole state directory can still
// come back, as long as one phone connects and hands over the D1 token.
//
// The daemon owns no credential. TokenFunc returns the token a phone presented
// in a verified Authorization header; it is kept in memory by the transport and
// is empty until the first successful connection (CON-012).
package d1store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// ErrTokenRejected marks a wrong credential, as opposed to a transport
// problem. Callers count only the former as failed logins.
var ErrTokenRejected = errors.New("d1store: token rejected")

// ErrNoToken means nobody has connected yet: the daemon holds no credential of
// its own, so D1 is unreachable until a phone presents one.
var ErrNoToken = errors.New("d1store: no D1 token yet (waiting for a phone)")

const (
	defaultTimeout   = 30 * time.Second
	maxValueBytes    = 450_000 // stay under the Worker's 500 KB value limit
	SessionKeyPrefix = "sessions/"
)

// Client is a small Worker REST client. It is deliberately dependency-free so
// the runtime keeps using its own HTTP conventions.
type Client struct {
	base    string
	http    *http.Client
	tokenFn func() string
}

func NewClient(base string, tokenFn func() string) *Client {
	if strings.TrimSpace(base) == "" {
		return nil
	}
	if tokenFn == nil {
		tokenFn = func() string { return "" }
	}
	return &Client{
		base:    strings.TrimRight(base, "/"),
		http:    &http.Client{Timeout: defaultTimeout},
		tokenFn: tokenFn,
	}
}

func (c *Client) do(ctx context.Context, token, method, path string, body []byte, out any) error {
	if c == nil {
		return errors.New("d1store: client is not configured")
	}
	if strings.TrimSpace(token) == "" {
		return ErrNoToken
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "ai")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w (HTTP %d)", ErrTokenRejected, resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return fmt.Errorf("d1store: %s %s -> HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// VerifyToken checks a candidate credential (from a phone handshake) against
// the Worker. The caller decides whether to adopt it.
func (c *Client) VerifyToken(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return ErrTokenRejected
	}
	return c.do(ctx, token, http.MethodGet, "/api/ping", nil, nil)
}

// CurrentToken reports the token the transport currently holds ("" = none).
func (c *Client) CurrentToken() string {
	if c == nil || c.tokenFn == nil {
		return ""
	}
	return c.tokenFn()
}

// Get returns one state value. found=false means the key does not exist.
func (c *Client) Get(ctx context.Context, key string) (value string, found bool, err error) {
	var out struct {
		Value *string `json:"value"`
	}
	if err := c.do(ctx, c.CurrentToken(), http.MethodGet, "/api/state/"+url.PathEscape(key), nil, &out); err != nil {
		return "", false, err
	}
	if out.Value == nil {
		return "", false, nil
	}
	return *out.Value, true, nil
}

// Put stores a state value, refusing oversized payloads instead of letting the
// Worker reject them mid-turn (CON-012).
func (c *Client) Put(ctx context.Context, key, value string) error {
	if len(value) > maxValueBytes {
		return fmt.Errorf("d1store: %q is %d bytes, over the %d byte limit", key, len(value), maxValueBytes)
	}
	body, err := json.Marshal(map[string]string{"value": value})
	if err != nil {
		return err
	}
	return c.do(ctx, c.CurrentToken(), http.MethodPut, "/api/state/"+url.PathEscape(key), body, nil)
}

// List returns keys under a prefix, e.g. SessionKeyPrefix.
func (c *Client) List(ctx context.Context, prefix string) ([]string, error) {
	var out struct {
		Keys []string `json:"keys"`
	}
	path := "/api/state?prefix=" + url.QueryEscape(prefix)
	if err := c.do(ctx, c.CurrentToken(), http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Keys, nil
}

// AppendTurn mirrors one finished turn into the history table the phone reads.
// order=0 means "append at the end"; positive values keep FIFO ingestion when
// several turns are flushed together.
func (c *Client) AppendTurn(ctx context.Context, sessionID, role, agent, jobID, text string) error {
	body, err := json.Marshal(map[string]string{
		"role": role, "agent": agent, "job_id": jobID, "text": text,
	})
	if err != nil {
		return err
	}
	path := "/api/sessions/" + url.PathEscape(sessionID) + "/turns"
	return c.do(ctx, c.CurrentToken(), http.MethodPost, path, body, nil)
}

// Heartbeat announces the public quick-tunnel URL so the phone can discover
// this daemon through the Worker instead of a hardcoded host.
func (c *Client) Heartbeat(ctx context.Context, tunnelURL, version string) error {
	body, err := json.Marshal(map[string]string{"tunnel_url": tunnelURL, "version": version})
	if err != nil {
		return err
	}
	return c.do(ctx, c.CurrentToken(), http.MethodPost, "/api/node/heartbeat", body, nil)
}

// MemoryToken stores the D1 token in RAM. It is the daemon's only credential
// and it is intentionally volatile: a restart requires a phone to hand it over
// again (REQ-046(3), CON-012).
type MemoryToken struct{ value atomic.Value }

func NewMemoryToken() *MemoryToken {
	t := &MemoryToken{}
	t.value.Store("")
	return t
}

func (m *MemoryToken) Get() string {
	if m == nil {
		return ""
	}
	v, _ := m.value.Load().(string)
	return v
}

// Adopt records a token only after the caller verified it with the Worker.
func (m *MemoryToken) Adopt(token string) {
	if m == nil || strings.TrimSpace(token) == "" {
		return
	}
	m.value.Store(strings.TrimSpace(token))
}

func (m *MemoryToken) Clear() {
	if m != nil {
		m.value.Store("")
	}
}
