package runtime

import (
	"context"
	"errors"
	"sync"

	"github.com/Tulipskun/ai/sdk"
)

type SessionManager struct { path string; base sdk.SessionConfig; keys *sdk.KeyPool; providerKeys map[sdk.ProviderID]*sdk.KeyPool; mu sync.Mutex; sessions map[string]*sdk.Session }
func NewSessionManager(path string, base sdk.SessionConfig, keys *sdk.KeyPool) *SessionManager { return &SessionManager{path: path, base: base, keys: keys, sessions: make(map[string]*sdk.Session)} }
func NewSessionManagerWithProviders(path string, base sdk.SessionConfig, providers []sdk.ProviderConfig) *SessionManager {
	providerKeys := make(map[sdk.ProviderID]*sdk.KeyPool, len(providers)); var fallback *sdk.KeyPool
	for _, provider := range providers { if provider.Keys == nil { continue }; providerKeys[provider.ID] = provider.Keys; if fallback == nil { fallback = provider.Keys } }
	if base.Provider != "" && providerKeys[base.Provider] != nil { fallback = providerKeys[base.Provider] }
	return &SessionManager{path: path, base: base, keys: fallback, providerKeys: providerKeys, sessions: make(map[string]*sdk.Session)}
}
func (m *SessionManager) RegisterProvider(provider sdk.ProviderID, keys *sdk.KeyPool) {
	if m == nil || provider == "" || keys == nil { return }
	m.mu.Lock()
	m.providerKeys[provider] = keys
	m.mu.Unlock()
}
func (m *SessionManager) Resolve(ctx context.Context, input sdk.Input) (*sdk.Session, error) {
	if m == nil { return nil, errors.New("runtime: session manager is nil") }; if err := ctx.Err(); err != nil { return nil, err }; if input.SessionID == "" { return nil, errors.New("runtime: input SessionID is required") }
	m.mu.Lock(); defer m.mu.Unlock(); if session, ok := m.sessions[input.SessionID]; ok { return session, nil }
	config := m.base; config.ID = input.SessionID; keys := m.keys; if providerKeys := m.providerKeys[config.Provider]; providerKeys != nil { keys = providerKeys }
	session, err := sdk.OpenSession(m.path, config, keys); if err != nil { return nil, err }
	loadedProvider := session.Config().Provider
	if providerKeys := m.providerKeys[loadedProvider]; providerKeys != nil { if err := session.SetKeyPool(providerKeys); err != nil { _ = session.Close(); return nil, err } }
	m.sessions[input.SessionID] = session; return session, nil
}
func (m *SessionManager) Close() error { if m == nil { return nil }; m.mu.Lock(); defer m.mu.Unlock(); var firstErr error; for id, session := range m.sessions { if err := session.Close(); err != nil && firstErr == nil { firstErr = err }; delete(m.sessions, id) }; return firstErr }
