package sdk

import (
	"errors"
	"fmt"
	"sync"
)

type routeKey struct {
	provider ProviderID
	model    string
}

type Router struct {
	mu     sync.RWMutex
	routes map[routeKey]ModelRoute
}

func NewRouter() *Router { return &Router{routes: make(map[routeKey]ModelRoute)} }

func (r *Router) Register(route ModelRoute) {
	if route.Provider == "" || route.Model == "" || route.Adapter == "" {
		panic("sdk: invalid model route")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes[routeKey{route.Provider, route.Model}] = route
}

func (r *Router) Resolve(provider ProviderID, model string) (ModelRoute, error) {
	if provider == "" {
		return ModelRoute{}, errors.New("sdk: provider is required")
	}
	if model == "" {
		return ModelRoute{}, errors.New("sdk: model is required")
	}
	r.mu.RLock()
	route, ok := r.routes[routeKey{provider, model}]
	r.mu.RUnlock()
	if !ok {
		return ModelRoute{}, fmt.Errorf("sdk: no route for provider=%q model=%q", provider, model)
	}
	return route, nil
}

type Session struct {
	config SessionConfig
	keys   *KeyPool
}

func NewSession(config SessionConfig, keys *KeyPool) *Session {
	return &Session{config: config, keys: keys}
}

func (s *Session) ID() string             { return s.config.ID }
func (s *Session) Config() SessionConfig { return s.config }

func (s *Session) APIKey() (string, error) {
	if s.keys == nil {
		return "", errors.New("sdk: session has no key pool")
	}
	return s.keys.At(s.config.KeyIndex)
}
