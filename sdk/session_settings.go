package sdk

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// This module writes settings to the active agent side: the top-level fields
// for main, the Sub overlay for sub (REQ-030). Sessions default to main, so
// existing callers keep their behavior. SetKeyPool keeps its legacy meaning
// (the main pool); sub pools travel through SetProvider and SetSubKeyPool.

func (s *Session) isSubLocked() bool { return s.config.AgentMode == AgentModeSub }

func (s *Session) SetKeyPool(keys *KeyPool) error {
	if s == nil {
		return errors.New("sdk: session is nil")
	}
	if keys == nil {
		return errors.New("sdk: provider has no key pool")
	}
	if _, err := keys.At(0); err != nil {
		return err
	}
	s.mu.Lock()
	s.keys = keys
	s.mu.Unlock()
	return nil
}

func (s *Session) SetSubKeyPool(keys *KeyPool) error {
	if s == nil {
		return errors.New("sdk: session is nil")
	}
	if keys == nil {
		return errors.New("sdk: provider has no key pool")
	}
	if _, err := keys.At(0); err != nil {
		return err
	}
	s.mu.Lock()
	s.subKeys = keys
	s.mu.Unlock()
	return nil
}

func (s *Session) SetProvider(provider ProviderID, keys *KeyPool) error {
	provider = ProviderID(strings.TrimSpace(string(provider)))
	if provider == "" {
		return errors.New("sdk: provider is required")
	}
	if err := checkPool(keys); err != nil {
		return err
	}
	return s.updateConfig(func(config *SessionConfig) error {
		if s.isSubLocked() {
			config.Sub.Provider = provider
			config.Sub.Model = ""
			config.Sub.KeyIndex = 0
			s.subKeys = keys
		} else {
			config.Provider = provider
			config.Model = ""
			config.KeyIndex = 0
			s.keys = keys
		}
		return nil
	})
}

func checkPool(keys *KeyPool) error {
	if keys == nil {
		return errors.New("sdk: provider has no key pool")
	}
	if _, err := keys.At(0); err != nil {
		return err
	}
	return nil
}

func (s *Session) SetModel(model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return errors.New("sdk: model is required")
	}
	return s.updateConfig(func(config *SessionConfig) error {
		if s.isSubLocked() {
			config.Sub.Model = model
		} else {
			config.Model = model
		}
		return nil
	})
}
func (s *Session) SetTemperature(temperature float64) error {
	if math.IsNaN(temperature) || math.IsInf(temperature, 0) {
		return errors.New("sdk: temperature must be finite")
	}
	return s.updateConfig(func(config *SessionConfig) error {
		v := temperature
		if s.isSubLocked() {
			config.Sub.Temperature = &v
		} else {
			config.Temperature = &v
		}
		return nil
	})
}
func (s *Session) ClearTemperature() error {
	return s.updateConfig(func(config *SessionConfig) error {
		if s.isSubLocked() {
			config.Sub.Temperature = nil
		} else {
			config.Temperature = nil
		}
		return nil
	})
}
func (s *Session) SetThinkingLevel(level ThinkingLevel) error {
	if !validThinkingLevel(level) {
		return fmt.Errorf("sdk: invalid thinking level %q", level)
	}
	return s.updateConfig(func(config *SessionConfig) error {
		if s.isSubLocked() {
			config.Sub.ThinkingLevel = level
		} else {
			config.ThinkingLevel = level
		}
		return nil
	})
}
func (s *Session) ClearThinkingLevel() error {
	return s.updateConfig(func(config *SessionConfig) error {
		if s.isSubLocked() {
			config.Sub.ThinkingLevel = ""
		} else {
			config.ThinkingLevel = ""
		}
		return nil
	})
}
func (s *Session) SetKeyIndex(index int) error {
	if s == nil {
		return errors.New("sdk: session is nil")
	}
	if index < 0 {
		return errors.New("sdk: API key index out of range")
	}
	s.mu.RLock()
	pool := s.activeKeys()
	s.mu.RUnlock()
	if pool == nil {
		return errors.New("sdk: session has no key pool")
	}
	if _, err := pool.At(index); err != nil {
		return err
	}
	return s.updateConfig(func(config *SessionConfig) error {
		if s.isSubLocked() {
			config.Sub.KeyIndex = index
		} else {
			config.KeyIndex = index
		}
		return nil
	})
}

// SetSubSettings replaces the whole sub side at once, used when cloning a
// session (for example /new). An empty provider clears the sub side.
func (s *Session) SetSubSettings(sub ModeSettings, keys *KeyPool) error {
	if s == nil {
		return errors.New("sdk: session is nil")
	}
	sub.Provider = ProviderID(strings.TrimSpace(string(sub.Provider)))
	sub.Model = strings.TrimSpace(sub.Model)
	if sub.Provider == "" {
		return s.updateConfig(func(config *SessionConfig) error {
			config.Sub = ModeSettings{}
			s.subKeys = nil
			return nil
		})
	}
	if err := checkPool(keys); err != nil {
		return err
	}
	if sub.Model == "" {
		return errors.New("sdk: model is required")
	}
	if _, err := keys.At(sub.KeyIndex); err != nil {
		return err
	}
	return s.updateConfig(func(config *SessionConfig) error {
		config.Sub = sub
		s.subKeys = keys
		return nil
	})
}

// EnsureSubSettings seeds an empty sub side from the main side, so entering
// sub mode starts from the current settings and the user diverges from
// there. A configured sub side is left untouched.
func (s *Session) EnsureSubSettings(keys *KeyPool) error {
	if s == nil {
		return errors.New("sdk: session is nil")
	}
	if err := checkPool(keys); err != nil {
		return err
	}
	s.mu.RLock()
	seeded := s.config.Sub.Provider != ""
	s.mu.RUnlock()
	if seeded {
		return nil
	}
	return s.updateConfig(func(config *SessionConfig) error {
		if config.Sub.Provider != "" {
			return nil
		}
		config.Sub = ModeSettings{Provider: config.Provider, Model: config.Model, KeyIndex: config.KeyIndex, ThinkingLevel: config.ThinkingLevel, Temperature: config.Temperature}
		s.subKeys = keys
		return nil
	})
}
func (s *Session) SetAgentMode(mode AgentMode) error {
	if s == nil {
		return errors.New("sdk: session is nil")
	}
	switch mode {
	case AgentModeMain, AgentModeSub:
		return s.updateConfig(func(config *SessionConfig) error { config.AgentMode = mode; return nil })
	default:
		return fmt.Errorf("sdk: invalid agent mode %q", mode)
	}
}
func validThinkingLevel(level ThinkingLevel) bool {
	switch level {
	case ThinkingNone, ThinkingLow, ThinkingMedium, ThinkingHigh:
		return true
	default:
		return false
	}
}
func (s *Session) updateConfig(update func(*SessionConfig) error) error { if s == nil { return errors.New("sdk: session is nil") }; s.mu.Lock(); defer s.mu.Unlock(); config := s.config; if config.Temperature != nil { v := *config.Temperature; config.Temperature = &v }; if config.Sub.Temperature != nil { v := *config.Sub.Temperature; config.Sub.Temperature = &v }; if err := update(&config); err != nil { return err }; if s.store != nil { if err := s.store.SaveSession(config); err != nil { return err } }; s.config = config; return nil }
