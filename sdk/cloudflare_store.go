package sdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var ErrCloudflareNotFound = errors.New("sdk: cloudflare record not found")

type CloudflareStoreConfig struct {
	BaseURL string
	Token   string
	Timeout time.Duration
}

type CloudflareSessionStore struct {
	config    CloudflareStoreConfig
	sessionID string
	client    *http.Client
	nextID    atomic.Int64
}

type cloudflareSessionEnvelope struct {
	Config  SessionConfig `json:"config"`
	History []Turn `json:"history"`
	Usage   Usage `json:"usage"`
}

type cloudflareSessionListEnvelope struct {
	Sessions []struct {
		ID        string `json:"id"`
		Provider  string `json:"provider"`
		Model     string `json:"model"`
		UpdatedAt string `json:"updated_at"`
		TurnCount int    `json:"turn_count"`
	} `json:"sessions"`
}

func NewCloudflareSessionStore(config CloudflareStoreConfig, sessionID string) (*CloudflareSessionStore, error) {
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	config.Token = strings.TrimSpace(config.Token)
	if config.BaseURL == "" { return nil, errors.New("sdk: cloudflare base URL is required") }
	if config.Token == "" { return nil, errors.New("sdk: cloudflare compute token is required") }
	if sessionID == "" { return nil, errors.New("sdk: cloudflare session ID is required") }
	if config.Timeout <= 0 { config.Timeout = 15 * time.Second }
	return &CloudflareSessionStore{config: config, sessionID: sessionID, client: &http.Client{Timeout: config.Timeout}}, nil
}

func OpenCloudflareSession(config CloudflareStoreConfig, sessionConfig SessionConfig, keys *KeyPool) (*Session, error) {
	store, err := NewCloudflareSessionStore(config, sessionConfig.ID)
	if err != nil { return nil, err }
	return openSessionWithStore(sessionConfig, keys, store)
}

func ListCloudflareSessions(config CloudflareStoreConfig, limit int) ([]SessionInfo, error) {
	if limit <= 0 { limit = 20 }
	store, err := NewCloudflareSessionStore(config, "list")
	if err != nil { return nil, err }
	var out cloudflareSessionListEnvelope
	if err := store.request(http.MethodGet, "/v1/compute/storage/sessions?limit="+strconv.Itoa(limit), nil, &out); err != nil { return nil, err }
	items := make([]SessionInfo, 0, len(out.Sessions))
	for _, row := range out.Sessions {
		item := SessionInfo{ID: row.ID, Provider: ProviderID(row.Provider), Model: row.Model, TurnCount: row.TurnCount}
		if parsed, err := time.Parse(time.RFC3339Nano, row.UpdatedAt); err == nil { item.UpdatedAt = parsed }
		items = append(items, item)
	}
	return items, nil
}

func (s *CloudflareSessionStore) Close() error { return nil }

func (s *CloudflareSessionStore) SaveSession(config SessionConfig) error {
	var history []Turn
	var usage Usage
	if config.ID == s.sessionID {
		if current, err := s.loadEnvelope(config.ID); err == nil { history, usage = current.History, current.Usage }
	}
	return s.request(http.MethodPut, "/v1/compute/storage/sessions/"+escapePath(config.ID), cloudflareSessionEnvelope{Config: config, History: history, Usage: usage}, nil)
}

func (s *CloudflareSessionStore) LoadSession(sessionID string) (SessionConfig, error) {
	envelope, err := s.loadEnvelope(sessionID); if err != nil { return SessionConfig{}, err }; return envelope.Config, nil
}
func (s *CloudflareSessionStore) LoadUsage(sessionID string) (Usage, error) {
	envelope, err := s.loadEnvelope(sessionID); if err != nil { return Usage{}, err }; return envelope.Usage, nil
}
func (s *CloudflareSessionStore) LoadHistory(sessionID string) ([]Turn, error) {
	envelope, err := s.loadEnvelope(sessionID); if err != nil { return nil, err }; return envelope.History, nil
}
func (s *CloudflareSessionStore) AppendTurns(sessionID string, turns []Turn, startSeq int) error {
	if len(turns) == 0 { return nil }
	envelope, err := s.loadEnvelope(sessionID); if err != nil { return err }
	envelope.History = append(envelope.History, turns...)
	return s.request(http.MethodPut, "/v1/compute/storage/sessions/"+escapePath(sessionID), envelope, nil)
}
func (s *CloudflareSessionStore) ReplaceTurns(sessionID string, turns []Turn) error {
	envelope, err := s.loadEnvelope(sessionID); if err != nil { return err }
	envelope.History = turns
	return s.request(http.MethodPut, "/v1/compute/storage/sessions/"+escapePath(sessionID), envelope, nil)
}
func (s *CloudflareSessionStore) RecordRequest(sessionID string, attempt int, req Request) (int64, error) {
	return time.Now().UnixNano()/int64(time.Millisecond)*1000 + s.nextID.Add(1), nil
}
func (s *CloudflareSessionStore) RecordResponse(requestID int64, resp Response, err error) error {
	envelope, loadErr := s.loadEnvelope(s.sessionID); if loadErr != nil { return loadErr }
	envelope.Usage.InputTokens += resp.Usage.InputTokens
	envelope.Usage.OutputTokens += resp.Usage.OutputTokens
	envelope.Usage.TotalTokens += resp.Usage.TotalTokens
	envelope.Usage.CacheReadTokens += resp.Usage.CacheReadTokens
	envelope.Usage.CacheWriteTokens += resp.Usage.CacheWriteTokens
	return s.request(http.MethodPut, "/v1/compute/storage/sessions/"+escapePath(s.sessionID), envelope, nil)
}
func (s *CloudflareSessionStore) loadEnvelope(sessionID string) (cloudflareSessionEnvelope, error) {
	var envelope cloudflareSessionEnvelope
	if err := s.request(http.MethodGet, "/v1/compute/storage/sessions/"+escapePath(sessionID), nil, &envelope); err != nil { return cloudflareSessionEnvelope{}, err }
	return envelope, nil
}
func (s *CloudflareSessionStore) request(method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil { data, err := json.Marshal(body); if err != nil { return err }; reader = bytes.NewReader(data) }
	req, err := http.NewRequest(method, s.config.BaseURL+path, reader); if err != nil { return err }
	req.Header.Set("Authorization", "Bearer "+s.config.Token)
	if body != nil { req.Header.Set("Content-Type", "application/json") }
	resp, err := s.client.Do(req); if err != nil { return err }
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)); if err != nil { return err }
	if resp.StatusCode == http.StatusNotFound { return ErrCloudflareNotFound }
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(data)); if len(message) > 240 { message = message[:240] }
		return fmt.Errorf("sdk: cloudflare storage HTTP %d: %s", resp.StatusCode, message)
	}
	if out == nil || len(data) == 0 { return nil }
	if err := json.Unmarshal(data, out); err != nil { return fmt.Errorf("sdk: decode cloudflare storage response: %w", err) }
	return nil
}
func escapePath(value string) string { return strings.ReplaceAll(value, "/", "%2F") }
