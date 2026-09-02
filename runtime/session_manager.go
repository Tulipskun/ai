package runtime

import (
	"context"
	"errors"
	"sync"

	"github.com/Tulipskun/ai/sdk"
)

// SessionManager owns transport-independent session identity and durable
// session instances. The transport decides the SessionID; the manager does not
// interpret Discord, Telegram, Web, or other source-specific identifiers.
type SessionManager struct {
	path string
	base sdk.SessionConfig
	keys *sdk.KeyPool

	mu       sync.Mutex
	sessions map[string]*sdk.Session
}

func NewSessionManager(path string, base sdk.SessionConfig, keys *sdk.KeyPool) *SessionManager {
	return &SessionManager{path: path, base: base, keys: keys, sessions: make(map[string]*sdk.Session)}
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
	if session, ok := m.sessions[input.SessionID]; ok {
		return session, nil
	}

	config := m.base
	config.ID = input.SessionID
	session, err := sdk.OpenSession(m.path, config, m.keys)
	if err != nil {
		return nil, err
	}
	m.sessions[input.SessionID] = session
	return session, nil
}

func (m *SessionManager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	for id, session := range m.sessions {
		if err := session.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(m.sessions, id)
	}
	return firstErr
}
