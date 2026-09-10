package discord

import (
	"errors"
	"strings"
	"sync"
)

// SessionMapping contains the Discord-specific channel -> session mapping.
type SessionMapping struct {
	mu       sync.RWMutex
	channels map[string]string
}

func NewSessionMapping() *SessionMapping {
	return &SessionMapping{channels: make(map[string]string)}
}

func (m *SessionMapping) Set(channelID, sessionID string) error {
	if m == nil {
		return errors.New("discord: session mapping is nil")
	}
	channelID = strings.TrimSpace(channelID)
	sessionID = strings.TrimSpace(sessionID)
	if channelID == "" {
		return errors.New("discord: channel ID is required")
	}
	if sessionID == "" {
		return errors.New("discord: session ID is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[channelID] = sessionID
	return nil
}

func (m *SessionMapping) Delete(channelID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.channels, strings.TrimSpace(channelID))
}

func (m *SessionMapping) Get(channelID string) (string, bool) {
	if m == nil {
		return "", false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	sessionID, ok := m.channels[strings.TrimSpace(channelID)]
	return sessionID, ok
}

func (m *SessionMapping) SessionID(channelID string) string {
	if sessionID, ok := m.Get(channelID); ok {
		return sessionID
	}
	return "discord:channel:" + strings.TrimSpace(channelID)
}
