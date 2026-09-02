package sdk

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type routeKey struct { provider ProviderID; model string }

type Router struct {
	mu sync.RWMutex
	routes map[routeKey]ModelRoute
	providers map[ProviderID]ProviderConfig
	catalogs map[ProviderID][]Model
	catalogReady map[ProviderID]bool
}

func NewRouter() *Router { return &Router{routes: make(map[routeKey]ModelRoute), providers: make(map[ProviderID]ProviderConfig), catalogs: make(map[ProviderID][]Model), catalogReady: make(map[ProviderID]bool)} }

func (r *Router) RegisterProvider(config ProviderConfig) {
	if config.ID == "" || config.Adapter == "" { panic("sdk: invalid provider config") }
	r.mu.Lock(); defer r.mu.Unlock(); r.providers[config.ID] = config; delete(r.catalogs, config.ID); r.catalogReady[config.ID] = false
}
func (r *Router) Provider(provider ProviderID) (ProviderConfig, error) { r.mu.RLock(); config, ok := r.providers[provider]; r.mu.RUnlock(); if !ok { return ProviderConfig{}, fmt.Errorf("sdk: provider %q is not registered", provider) }; return config, nil }
func (r *Router) Register(route ModelRoute) { if route.Provider == "" || route.Model == "" || route.Adapter == "" { panic("sdk: invalid model route") }; r.mu.Lock(); defer r.mu.Unlock(); r.routes[routeKey{route.Provider, route.Model}] = route }

func (r *Router) RefreshModels(ctx context.Context, provider ProviderID, adapter Provider) error {
	config, err := r.Provider(provider); if err != nil { return err }
	lister, ok := adapter.(ModelLister); if !ok { return fmt.Errorf("sdk: adapter %q does not support model discovery", config.Adapter) }
	if config.Keys == nil { return errors.New("sdk: provider has no key pool") }
	key, err := config.Keys.Current(); if err != nil { return err }
	p := adapter
	if kp, ok := p.(KeyedProvider); ok { p = kp.WithAPIKey(key) }
	if ep, ok := p.(EndpointProvider); ok && config.BaseURL != "" { p = ep.WithBaseURL(config.BaseURL) }
	lister, ok = p.(ModelLister); if !ok { return fmt.Errorf("sdk: configured adapter %q cannot discover models after configuration", config.Adapter) }
	models, err := lister.ListModels(ctx, key); if err != nil { return err }
	clean := make([]Model, 0, len(models)); seen := make(map[string]struct{}, len(models))
	for _, model := range models { if model.ID == "" { continue }; if _, exists := seen[model.ID]; exists { continue }; seen[model.ID] = struct{}{}; clean = append(clean, model) }
	r.mu.Lock(); r.catalogs[provider] = clean; r.catalogReady[provider] = true; r.mu.Unlock(); return nil
}
func (r *Router) Models(provider ProviderID) []Model { r.mu.RLock(); models := append([]Model(nil), r.catalogs[provider]...); r.mu.RUnlock(); return models }
func (r *Router) Resolve(provider ProviderID, model string) (ModelRoute, error) {
	if provider == "" { return ModelRoute{}, errors.New("sdk: provider is required") }; if model == "" { return ModelRoute{}, errors.New("sdk: model is required") }
	r.mu.RLock(); config, providerOK := r.providers[provider]; ready := r.catalogReady[provider]; route, staticOK := r.routes[routeKey{provider, model}]
	if ready { for _, discovered := range r.catalogs[provider] { if discovered.ID == model { r.mu.RUnlock(); return ModelRoute{Provider: provider, Model: model, Adapter: config.Adapter}, nil } }; r.mu.RUnlock(); return ModelRoute{}, fmt.Errorf("sdk: model %q is not available for provider=%q", model, provider) }
	r.mu.RUnlock(); if staticOK { return route, nil }; if providerOK { return ModelRoute{}, fmt.Errorf("sdk: model catalogue for provider=%q has not been refreshed", provider) }; return ModelRoute{}, fmt.Errorf("sdk: no route for provider=%q model=%q", provider, model)
}

type Session struct { mu sync.RWMutex; config SessionConfig; keys *KeyPool; history []Turn }
func NewSession(config SessionConfig, keys *KeyPool) *Session { return &Session{config: config, keys: keys} }
func (s *Session) ID() string { return s.config.ID }
func (s *Session) Config() SessionConfig { s.mu.RLock(); defer s.mu.RUnlock(); c := s.config; if c.Temperature != nil { v := *c.Temperature; c.Temperature = &v }; return c }
func (s *Session) APIKey() (string, error) { s.mu.RLock(); keys, index := s.keys, s.config.KeyIndex; s.mu.RUnlock(); if keys == nil { return "", errors.New("sdk: session has no key pool") }; return keys.At(index) }

// History returns a defensive copy of this session's canonical conversation history.
func (s *Session) History() []Turn { s.mu.RLock(); defer s.mu.RUnlock(); return cloneTurns(s.history) }

// Append commits turns. Provider failures should never be appended as model history.
func (s *Session) Append(turns ...Turn) { s.mu.Lock(); defer s.mu.Unlock(); s.history = repairTurns(append(s.history, cloneTurns(turns)...)) }

// ReplaceHistory replaces the session history after repairing it.
func (s *Session) ReplaceHistory(turns []Turn) { s.mu.Lock(); defer s.mu.Unlock(); s.history = repairTurns(cloneTurns(turns)) }

// RepairHistory removes orphan tool results, duplicate tool IDs, and incomplete tool calls.
func (s *Session) RepairHistory() { s.mu.Lock(); defer s.mu.Unlock(); s.history = repairTurns(s.history) }

func cloneTurns(in []Turn) []Turn { out := make([]Turn, len(in)); copy(out, in); for i := range out { out[i].Content = append([]ContentPart(nil), out[i].Content...); if out[i].ToolCall != nil { v := *out[i].ToolCall; out[i].ToolCall = &v }; if out[i].ToolResult != nil { v := *out[i].ToolResult; out[i].ToolResult = &v } }; return out }

func repairTurns(in []Turn) []Turn {
	out := make([]Turn, 0, len(in)); pending := make(map[string]bool)
	for _, turn := range in {
		switch turn.Role {
		case RoleUser, RoleModel: out = append(out, turn)
		case RoleToolCall:
			if turn.ToolCall == nil || turn.ToolCall.ID == "" || turn.ToolCall.Name == "" || pending[turn.ToolCall.ID] { continue }; pending[turn.ToolCall.ID] = true; out = append(out, turn)
		case RoleToolResult:
			if turn.ToolResult == nil || turn.ToolResult.ID == "" || !pending[turn.ToolResult.ID] { continue }; delete(pending, turn.ToolResult.ID); out = append(out, turn)
		}
	}
	if len(pending) == 0 { return out }
	final := out[:0]; for _, turn := range out { if turn.Role == RoleToolCall && pending[turn.ToolCall.ID] { continue }; final = append(final, turn) }; return final
}
