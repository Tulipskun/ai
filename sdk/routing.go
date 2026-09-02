package sdk

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type routeKey struct {
	provider ProviderID
	model    string
}

type Router struct {
	mu           sync.RWMutex
	routes       map[routeKey]ModelRoute
	providers    map[ProviderID]ProviderConfig
	catalogs     map[ProviderID][]Model
	catalogReady map[ProviderID]bool
}

func NewRouter() *Router {
	return &Router{
		routes:       make(map[routeKey]ModelRoute),
		providers:    make(map[ProviderID]ProviderConfig),
		catalogs:     make(map[ProviderID][]Model),
		catalogReady: make(map[ProviderID]bool),
	}
}

func (r *Router) RegisterProvider(config ProviderConfig) {
	if config.ID == "" || config.Adapter == "" {
		panic("sdk: invalid provider config")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[config.ID] = config
	delete(r.catalogs, config.ID)
	r.catalogReady[config.ID] = false
}

func (r *Router) Provider(provider ProviderID) (ProviderConfig, error) {
	r.mu.RLock()
	config, ok := r.providers[provider]
	r.mu.RUnlock()
	if !ok {
		return ProviderConfig{}, fmt.Errorf("sdk: provider %q is not registered", provider)
	}
	return config, nil
}

// Register keeps static-route compatibility. Discovered model catalogues are preferred.
func (r *Router) Register(route ModelRoute) {
	if route.Provider == "" || route.Model == "" || route.Adapter == "" {
		panic("sdk: invalid model route")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes[routeKey{route.Provider, route.Model}] = route
}

// RefreshModels replaces the entire live catalogue for a provider. Models missing from the
// latest response therefore disappear from Resolve/Models immediately.
func (r *Router) RefreshModels(ctx context.Context, provider ProviderID, adapter Provider) error {
	config, err := r.Provider(provider)
	if err != nil {
		return err
	}
	lister, ok := adapter.(ModelLister)
	if !ok {
		return fmt.Errorf("sdk: adapter %q does not support model discovery", config.Adapter)
	}
	if config.Keys == nil {
		return errors.New("sdk: provider has no key pool")
	}
	key, err := config.Keys.Current()
	if err != nil {
		return err
	}
	p := adapter
	if kp, ok := p.(KeyedProvider); ok {
		p = kp.WithAPIKey(key)
	}
	if ep, ok := p.(EndpointProvider); ok && config.BaseURL != "" {
		p = ep.WithBaseURL(config.BaseURL)
	}
	lister, ok = p.(ModelLister)
	if !ok {
		return fmt.Errorf("sdk: configured adapter %q cannot discover models after configuration", config.Adapter)
	}
	models, err := lister.ListModels(ctx, key)
	if err != nil {
		return err
	}

	clean := make([]Model, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		if model.ID == "" {
			continue
		}
		if _, exists := seen[model.ID]; exists {
			continue
		}
		seen[model.ID] = struct{}{}
		clean = append(clean, model)
	}

	r.mu.Lock()
	r.catalogs[provider] = clean
	r.catalogReady[provider] = true
	r.mu.Unlock()
	return nil
}

func (r *Router) Models(provider ProviderID) []Model {
	r.mu.RLock()
	models := append([]Model(nil), r.catalogs[provider]...)
	r.mu.RUnlock()
	return models
}

func (r *Router) Resolve(provider ProviderID, model string) (ModelRoute, error) {
	if provider == "" {
		return ModelRoute{}, errors.New("sdk: provider is required")
	}
	if model == "" {
		return ModelRoute{}, errors.New("sdk: model is required")
	}
	r.mu.RLock()
	config, providerOK := r.providers[provider]
	ready := r.catalogReady[provider]
	route, staticOK := r.routes[routeKey{provider, model}]
	if ready {
		for _, discovered := range r.catalogs[provider] {
			if discovered.ID == model {
				r.mu.RUnlock()
				return ModelRoute{Provider: provider, Model: model, Adapter: config.Adapter}, nil
			}
		}
		r.mu.RUnlock()
		return ModelRoute{}, fmt.Errorf("sdk: model %q is not available for provider=%q", model, provider)
	}
	r.mu.RUnlock()
	if staticOK {
		return route, nil
	}
	if providerOK {
		return ModelRoute{}, fmt.Errorf("sdk: model catalogue for provider=%q has not been refreshed", provider)
	}
	return ModelRoute{}, fmt.Errorf("sdk: no route for provider=%q model=%q", provider, model)
}

type Session struct {
	config SessionConfig
	keys   *KeyPool
}

func NewSession(config SessionConfig, keys *KeyPool) *Session {
	return &Session{config: config, keys: keys}
}

func (s *Session) ID() string            { return s.config.ID }
func (s *Session) Config() SessionConfig { return s.config }

func (s *Session) APIKey() (string, error) {
	if s.keys == nil {
		return "", errors.New("sdk: session has no key pool")
	}
	return s.keys.At(s.config.KeyIndex)
}
