package sdk

import (
	"context"
	"errors"
	"strings"
	"time"
)

type KeyedProvider interface { Provider; WithAPIKey(string) Provider }
type HeaderedProvider interface { Provider; WithHeaders(map[string]string) Provider }
// AdapterBinding identifies one adapter instance owned by one provider.
// A provider may serve models over different wire formats, so bindings are
// keyed by provider AND adapter: instances are never shared across
// providers, and one provider can still route different models through
// different adapters.
type AdapterBinding struct{ Provider ProviderID; Adapter AdapterID }
// RouterClient binds one adapter instance per provider. Adapters are never
// shared across providers: each provider owns its instance and per-request
// settings (key, base URL, headers) are applied on a copy.
type RouterClient struct { Router *Router; Adapters map[AdapterBinding]Provider }
func NewRouterClient(router *Router) *RouterClient { return &RouterClient{Router: router, Adapters: make(map[AdapterBinding]Provider)} }
func (c *RouterClient) RegisterAdapter(provider ProviderID, id AdapterID, p Provider) { c.Adapters[AdapterBinding{provider, id}] = p }
func (c *RouterClient) providerFor(session *Session, model string) (Provider, ModelRoute, error) {
	route, err := c.Router.Resolve(session.config.Provider, model)
	if err != nil && strings.Contains(err.Error(), "model catalogue for provider=") {
		provider := session.config.Provider
		if config, configErr := c.Router.Provider(provider); configErr == nil {
			if _, ok := c.Adapters[AdapterBinding{provider, config.Adapter}]; ok { if refreshErr := c.RefreshModels(context.Background(), provider); refreshErr == nil { route, err = c.Router.Resolve(provider, model) } else { err = refreshErr } }
		}
	}
	if err != nil { return nil, ModelRoute{}, err }
	p, ok := c.Adapters[AdapterBinding{route.Provider, route.Adapter}]
	if !ok { return nil, ModelRoute{}, &RouteError{Provider: route.Provider, Model: route.Model, Adapter: route.Adapter} }
	if kp, ok := p.(KeyedProvider); ok { key, err := session.APIKey(); if err != nil { return nil, ModelRoute{}, err }; p = kp.WithAPIKey(key) }
	if config, err := c.Router.Provider(route.Provider); err == nil { if config.BaseURL != "" { if ep, ok := p.(EndpointProvider); ok { p = ep.WithBaseURL(config.BaseURL) } }; if len(config.Headers) > 0 { if hp, ok := p.(HeaderedProvider); ok { p = hp.WithHeaders(config.Headers) } } }
	return p, route, nil
}
type RouteError struct { Provider ProviderID; Model string; Adapter AdapterID }
func (e *RouteError) Error() string { return "sdk: adapter not registered for provider=" + string(e.Provider) + " model=" + e.Model + " adapter=" + string(e.Adapter) }
func (c *RouterClient) RefreshModels(ctx context.Context, provider ProviderID) error { config, err := c.Router.Provider(provider); if err != nil { return err }; adapter, ok := c.Adapters[AdapterBinding{provider, config.Adapter}]; if !ok { return &RouteError{Provider: provider, Adapter: config.Adapter} }; return c.Router.RefreshModels(ctx, provider, adapter) }
func (c *RouterClient) Generate(ctx context.Context, session *Session, req Request) (Response, error) {
	cfg := session.Config(); model := req.Model; if model == "" { model = cfg.Model }
	p, route, err := c.providerFor(session, model); if err != nil { return Response{}, err }
	req.Provider, req.Model = route.Provider, route.Model
	if req.ConversationID == "" { req.ConversationID = session.ID() }
	if len(req.Messages) == 0 { req.Messages = session.History() }
	if req.ThinkingLevel == "" { req.ThinkingLevel = cfg.ThinkingLevel }
	if req.Temperature == nil && cfg.Temperature != nil { v := *cfg.Temperature; req.Temperature = &v }
	requestID, recordErr := session.RecordRequest(1, req); if recordErr != nil { return Response{}, recordErr }
	resp, err := p.Generate(ctx, req); resp.Provider, resp.Model = string(req.Provider), req.Model
	if recordErr := session.RecordResponse(requestID, resp, err); recordErr != nil { return Response{}, recordErr }
	if err != nil { session.RepairHistory(); return Response{}, err }
	return resp, nil
}
func retryable(err error) bool { if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) { return false }; var statusErr HTTPStatusError; if errors.As(err, &statusErr) { return RetryableHTTPStatus(statusErr.HTTPStatusCode()) }; return true }
func sleepBackoff(ctx context.Context, p RetryPolicy, attempt int, err error) error { d := p.InitialBackoff; if d <= 0 { d = 3 * time.Second }; maxWait := p.MaxBackoff; if maxWait <= 0 { maxWait = maxRetryCooldown }; for i := 1; i < attempt; i++ { d *= 2; if d >= maxWait { d = maxWait; break } }; if d > maxWait { d = maxWait }; if isRateLimitError(err) { if ra, ok := err.(RetryAfterError); ok { if rd := ra.RetryAfter(); rd > 0 && rd <= maxRetryCooldown { d = rd } } }; t := time.NewTimer(d); defer t.Stop(); select { case <-ctx.Done(): return ctx.Err(); case <-t.C: return nil } }
func (c *RouterClient) GenerateTurn(ctx context.Context, session *Session, user Turn, req Request) (Response, error) { before := session.History(); session.Append(user); req.Messages = session.History(); resp, err := c.Generate(ctx, session, req); if err != nil { session.ReplaceHistory(before); return Response{}, err }; commitResponse(session, resp); return resp, nil }
func commitResponse(session *Session, resp Response) { if len(resp.Content) > 0 || resp.Reasoning != nil { turn:=Turn{Role:RoleModel,Content:append([]ContentPart(nil),resp.Content...)}; if resp.Reasoning!=nil { r:=*resp.Reasoning; turn.Reasoning=&r }; session.Append(turn) }; for _, call := range resp.ToolCalls { call := call; session.Append(Turn{Role: RoleToolCall, ToolCall: &call}) } }
