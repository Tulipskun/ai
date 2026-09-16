package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type BrowserPage struct {
	TargetID string `json:"target_id"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	Title    string `json:"title"`
}

func (c *BrowserClient) Attach(ctx context.Context, endpoint string) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" { return errors.New("cdp endpoint is required") }
	versionURL, err := browserCDPHTTPURL(endpoint, "/json/version")
	if err != nil { return err }
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
	if err != nil { return err }
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil { return fmt.Errorf("connect to browser CDP: %w", err) }
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 { return fmt.Errorf("browser CDP returned HTTP %d", response.StatusCode) }
	var version struct{ WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"` }
	if err := json.NewDecoder(response.Body).Decode(&version); err != nil { return fmt.Errorf("decode browser CDP version: %w", err) }
	if version.WebSocketDebuggerURL == "" { return errors.New("browser CDP websocket URL missing") }
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, version.WebSocketDebuggerURL, nil)
	if err != nil { return fmt.Errorf("dial browser CDP: %w", err) }
	c.mu.Lock()
	if c.conn != nil { _ = c.conn.Close() }
	c.cmd = nil
	c.conn = conn
	c.ready = true
	c.mu.Unlock()
	return nil
}

func (c *BrowserClient) ListPages(ctx context.Context) ([]BrowserPage, error) {
	if err := c.ensureStarted(ctx); err != nil { return nil, err }
	endpoint, err := c.cdpHTTPBase()
	if err != nil { return nil, err }
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/json/list", nil)
	if err != nil { return nil, err }
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil { return nil, fmt.Errorf("list browser pages: %w", err) }
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 { return nil, fmt.Errorf("browser CDP returned HTTP %d", response.StatusCode) }
	var pages []struct { ID string `json:"id"`; Type string `json:"type"`; URL string `json:"url"`; Title string `json:"title"` }
	if err := json.NewDecoder(response.Body).Decode(&pages); err != nil { return nil, fmt.Errorf("decode browser pages: %w", err) }
	out := make([]BrowserPage, 0, len(pages))
	for _, page := range pages { if page.Type != "page" && page.Type != "webview" { continue }; out = append(out, BrowserPage{TargetID:page.ID,Type:page.Type,URL:page.URL,Title:page.Title}) }
	return out, nil
}

func (c *BrowserClient) AttachPage(ctx context.Context, sessionID, targetID string, out any) error {
	if strings.TrimSpace(sessionID) == "" { return errors.New("session_id is required") }
	if strings.TrimSpace(targetID) == "" { return errors.New("target_id is required") }
	if err := c.ensureStarted(ctx); err != nil { return err }
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if existing := c.sessions[sessionID]; existing != nil { return jsonInto(out, map[string]any{"session_id":sessionID,"context_id":sessionID,"page_id":existing.TargetID}) }
	var attached struct{ SessionID string `json:"sessionId"` }
	if err := c.command(ctx, "Target.attachToTarget", map[string]any{"targetId":targetID,"flatten":true}, "", &attached); err != nil { return err }
	if attached.SessionID == "" { return errors.New("browser CDP did not return a target session") }
	page := &browserSession{TargetID:targetID,CDPSession:attached.SessionID,Refs:make(map[string]browserRef),LastUsed:time.Now()}
	c.sessions[sessionID] = page
	meta, _ := c.pageMetadata(ctx, page)
	return jsonInto(out, map[string]any{"session_id":sessionID,"context_id":sessionID,"page_id":targetID,"url":meta.URL,"title":meta.Title,"attached":true})
}

func (c *BrowserClient) cdpHTTPBase() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil { return "", errors.New("browser is unavailable") }
	host := c.conn.RemoteAddr().String()
	if host == "" { return "", errors.New("browser CDP endpoint unavailable") }
	return "http://" + host, nil
}

func browserCDPHTTPURL(endpoint, suffix string) (string, error) {
	raw := strings.TrimSpace(endpoint)
	if !strings.Contains(raw, "://") { raw = "http://" + raw }
	u, err := url.Parse(raw)
	if err != nil { return "", fmt.Errorf("parse cdp endpoint: %w", err) }
	if u.Scheme != "http" && u.Scheme != "https" { return "", fmt.Errorf("unsupported cdp endpoint scheme %q", u.Scheme) }
	if u.Host == "" { return "", errors.New("cdp endpoint host is required") }
	base := strings.TrimRight(u.String(), "/")
	return base + suffix, nil
}
