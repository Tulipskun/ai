package runtime

import (
	"container/list"
	"context"
	"errors"
	"path/filepath"
	"sync"

	"github.com/Tulipskun/ai/sdk"
)

const defaultMaxCachedSessions = 8

type sessionCacheEntry struct {
	id      string
	session *sdk.Session
}
type SessionManager struct {
	dir          string
	base         sdk.SessionConfig
	keys         *sdk.KeyPool
	providerKeys map[sdk.ProviderID]*sdk.KeyPool
	mu           sync.Mutex
	sessions     map[string]*list.Element
	lru          *list.List
	maxCached    int
}

func sessionDir(path string) string {
	clean := filepath.Clean(path)
	if filepath.Ext(clean) == ".db" {
		return filepath.Join(filepath.Dir(clean), "sessions")
	}
	return clean
}

func NewSessionManager(path string, base sdk.SessionConfig, keys *sdk.KeyPool) *SessionManager {
	return &SessionManager{dir: sessionDir(path), base: base, keys: keys, providerKeys: make(map[sdk.ProviderID]*sdk.KeyPool), sessions: make(map[string]*list.Element), lru: list.New(), maxCached: defaultMaxCachedSessions}
}
func NewSessionManagerWithProviders(path string, base sdk.SessionConfig, providers []sdk.ProviderConfig) *SessionManager {
	providerKeys := make(map[sdk.ProviderID]*sdk.KeyPool, len(providers))
	var fallback *sdk.KeyPool
	for _, provider := range providers {
		if provider.Keys == nil {
			continue
		}
		providerKeys[provider.ID] = provider.Keys
		if fallback == nil {
			fallback = provider.Keys
		}
	}
	if base.Provider != "" && providerKeys[base.Provider] != nil {
		fallback = providerKeys[base.Provider]
	}
	return &SessionManager{dir: sessionDir(path), base: base, keys: fallback, providerKeys: providerKeys, sessions: make(map[string]*list.Element), lru: list.New(), maxCached: defaultMaxCachedSessions}
}

// AdoptProviders swaps in the provider set the daemon actually has after a
// config reload, and re-points sessions whose stored provider is gone. A
// session row hydrated from D1 keeps whatever provider wrote it, so without
// this a chat created before the restart would keep a provider that no longer
// exists and every turn on it fails with "provider is required" (REQ-046(4)).
func (m *SessionManager) AdoptProviders(configs []sdk.ProviderConfig, base sdk.SessionConfig) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if base.Provider != "" {
		m.base.Provider = base.Provider
	}
	if base.Model != "" {
		m.base.Model = base.Model
	}
	for _, config := range configs {
		if config.ID != "" && config.Keys != nil {
			m.providerKeys[config.ID] = config.Keys
		}
	}
	for _, entry := range m.sessions {
		_ = m.repointUnknownProviderLocked(entry.Value.(*sessionCacheEntry).session)
	}
}

func (m *SessionManager) repointUnknownProviderLocked(session *sdk.Session) error {
	if session == nil || m.base.Provider == "" {
		return nil
	}
	stored := session.Config().Provider
	if stored != "" && m.providerKeys[stored] != nil {
		return nil
	}
	keys := m.keys
	if providerKeys := m.providerKeys[m.base.Provider]; providerKeys != nil {
		keys = providerKeys
	}
	if err := session.SetProvider(m.base.Provider, keys); err != nil {
		return err
	}
	if m.base.Model != "" {
		return session.SetModel(m.base.Model)
	}
	return nil
}

func (m *SessionManager) RegisterProvider(provider sdk.ProviderID, keys *sdk.KeyPool) {
	if m == nil || provider == "" || keys == nil {
		return
	}
	m.mu.Lock()
	m.providerKeys[provider] = keys
	m.mu.Unlock()
}
func (m *SessionManager) Resolve(ctx context.Context, input sdk.Input) (*sdk.Session, error) {
	if m == nil {
		return nil, errors.New("runtime: session manager is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if input.SessionID == "" {
		return nil, errors.New("runtime: input SessionID is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if elem, ok := m.sessions[input.SessionID]; ok {
		m.lru.MoveToFront(elem)
		return elem.Value.(*sessionCacheEntry).session, nil
	}
	config := m.base
	config.ID = input.SessionID
	keys := m.keys
	if providerKeys := m.providerKeys[config.Provider]; providerKeys != nil {
		keys = providerKeys
	}
	path := sdk.SessionDBPath(m.dir, input.SessionID)
	session, err := sdk.OpenSession(path, config, keys)
	if err != nil {
		return nil, err
	}
	loadedProvider := session.Config().Provider
	if providerKeys := m.providerKeys[loadedProvider]; providerKeys != nil {
		if err := session.SetKeyPool(providerKeys); err != nil {
			_ = session.Close()
			return nil, err
		}
	}
	if err := m.repointUnknownProviderLocked(session); err != nil {
		_ = session.Close()
		return nil, err
	}
	elem := m.lru.PushFront(&sessionCacheEntry{id: input.SessionID, session: session})
	m.sessions[input.SessionID] = elem
	m.evictLocked()
	return session, nil
}
func (m *SessionManager) evictLocked() {
	limit := m.maxCached
	if limit <= 0 {
		limit = defaultMaxCachedSessions
	}
	for m.lru.Len() > limit {
		elem := m.lru.Back()
		if elem == nil {
			return
		}
		entry := elem.Value.(*sessionCacheEntry)
		delete(m.sessions, entry.id)
		m.lru.Remove(elem)
	}
}
func (m *SessionManager) ListSessions(limit int) ([]sdk.SessionInfo, error) {
	if m == nil {
		return nil, errors.New("runtime: session manager is nil")
	}
	return sdk.ListSessionsInDir(m.dir, limit)
}
func (m *SessionManager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	for id, elem := range m.sessions {
		entry := elem.Value.(*sessionCacheEntry)
		if err := entry.session.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(m.sessions, id)
	}
	m.lru.Init()
	return firstErr
}

// WorkspaceFor reports the persisted workspace of a live session without
// opening anything new. Empty means "use the process default" (REQ-038).
func (m *SessionManager) WorkspaceFor(sessionID string) string {
	if m == nil || sessionID == "" {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if elem, ok := m.sessions[sessionID]; ok {
		return elem.Value.(*sessionCacheEntry).session.Config().Workspace
	}
	return ""
}
