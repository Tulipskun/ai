package sdk

import (
	"context"
	"errors"
	"time"
)

type KeyedProvider interface { Provider; WithAPIKey(string) Provider }

type RouterClient struct {
	Router *Router
	Adapters map[AdapterID]Provider
	Retry RetryPolicy
}

func NewRouterClient(router *Router) *RouterClient { return &RouterClient{Router: router, Adapters: make(map[AdapterID]Provider), Retry: DefaultRetryPolicy()} }
func (c *RouterClient) RegisterAdapter(id AdapterID, p Provider) { c.Adapters[id] = p }

func (c *RouterClient) providerFor(session *Session) (Provider, ModelRoute, error) {
	route, err := c.Router.Resolve(session.config.Provider, session.config.Model); if err != nil { return nil, ModelRoute{}, err }
	p, ok := c.Adapters[route.Adapter]; if !ok { return nil, ModelRoute{}, &RouteError{Provider: route.Provider, Model: route.Model, Adapter: route.Adapter} }
	if kp, ok := p.(KeyedProvider); ok { key, err := session.APIKey(); if err != nil { return nil, ModelRoute{}, err }; p = kp.WithAPIKey(key) }
	if config, err := c.Router.Provider(route.Provider); err == nil && config.BaseURL != "" { if ep, ok := p.(EndpointProvider); ok { p = ep.WithBaseURL(config.BaseURL) } }
	return p, route, nil
}

type RouteError struct { Provider ProviderID; Model string; Adapter AdapterID }
func (e *RouteError) Error() string { return "sdk: adapter not registered for provider=" + string(e.Provider) + " model=" + e.Model + " adapter=" + string(e.Adapter) }

func (c *RouterClient) RefreshModels(ctx context.Context, provider ProviderID) error { config, err := c.Router.Provider(provider); if err != nil { return err }; adapter, ok := c.Adapters[config.Adapter]; if !ok { return &RouteError{Provider: provider, Adapter: config.Adapter} }; return c.Router.RefreshModels(ctx, provider, adapter) }

func (c *RouterClient) Generate(ctx context.Context, session *Session, req Request) (Response, error) {
	p, route, err := c.providerFor(session); if err != nil { return Response{}, err }
	req.Provider, req.Model = route.Provider, route.Model
	if len(req.Messages) == 0 { req.Messages = session.History() }
	if req.ThinkingLevel == "" { req.ThinkingLevel = session.Config().ThinkingLevel }
	if req.Temperature == nil && session.Config().Temperature != nil { v := *session.Config().Temperature; req.Temperature = &v }

	resp, err := c.generateRetry(ctx, p, req)
	if err != nil { session.RepairHistory(); return Response{}, err }
	resp.Provider, resp.Model = string(route.Provider), route.Model
	return resp, nil
}

func (c *RouterClient) generateRetry(ctx context.Context, p Provider, req Request) (Response, error) {
	policy := c.Retry; if policy.MaxAttempts <= 0 { policy.MaxAttempts = 1 }; if policy.InitialBackoff <= 0 { policy.InitialBackoff = 1 * time.Millisecond }; if policy.MaxBackoff <= 0 { policy.MaxBackoff = policy.InitialBackoff }
	var last error
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		resp, err := p.Generate(ctx, req); if err == nil { return resp, nil }; last = err
		if attempt == policy.MaxAttempts || !retryable(err) { break }
		if err := sleepBackoff(ctx, policy, attempt); err != nil { return Response{}, err }
	}
	return Response{}, last
}

func retryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) { return false }
	var statusErr HTTPStatusError
	if !errors.As(err, &statusErr) { return false }
	s := statusErr.HTTPStatusCode()
	return s == 429 || s == 500 || s == 502 || s == 503 || s == 504
}

func sleepBackoff(ctx context.Context, p RetryPolicy, attempt int) error {
	d := p.InitialBackoff
	for i := 1; i < attempt; i++ { if d >= p.MaxBackoff/2 { d = p.MaxBackoff; break }; d *= 2 }
	if d > p.MaxBackoff { d = p.MaxBackoff }
	t := time.NewTimer(d); defer t.Stop(); select { case <-ctx.Done(): return ctx.Err(); case <-t.C: return nil }
}

// GenerateTurn atomically adds a user turn and commits the model result only after success.
// If the provider fails after any number of retries, the session is restored to its exact prior history.
func (c *RouterClient) GenerateTurn(ctx context.Context, session *Session, user Turn, req Request) (Response, error) {
	before := session.History()
	session.Append(user)
	req.Messages = session.History()
	resp, err := c.Generate(ctx, session, req)
	if err != nil { session.ReplaceHistory(before); return Response{}, err }
	commitResponse(session, resp)
	return resp, nil
}

func commitResponse(session *Session, resp Response) {
	if len(resp.Content) > 0 { session.Append(Turn{Role: RoleModel, Content: append([]ContentPart(nil), resp.Content...)}) }
	for _, call := range resp.ToolCalls { call := call; session.Append(Turn{Role: RoleToolCall, ToolCall: &call}) }
}

func (c *RouterClient) Stream(ctx context.Context, session *Session, req Request) (<-chan Event, error) {
	p, route, err := c.providerFor(session); if err != nil { return nil, err }
	req.Provider, req.Model = route.Provider, route.Model
	if len(req.Messages) == 0 { req.Messages = session.History() }
	cfg := session.Config(); if req.ThinkingLevel == "" { req.ThinkingLevel = cfg.ThinkingLevel }; if req.Temperature == nil && cfg.Temperature != nil { v := *cfg.Temperature; req.Temperature = &v }
	return c.streamRetry(ctx, p, req)
}

func (c *RouterClient) streamRetry(ctx context.Context, p Provider, req Request) (<-chan Event, error) {
	policy := c.Retry; if policy.MaxAttempts <= 0 { policy.MaxAttempts = 1 }
	out := make(chan Event, 16)
	go func() {
		defer close(out)
		for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
			ch, err := p.Stream(ctx, req)
			if err != nil {
				if attempt < policy.MaxAttempts && retryable(err) { if err := sleepBackoff(ctx, policy, attempt); err == nil { continue } else { out <- Event{Type: EventError, Err: err}; return } }
				out <- Event{Type: EventError, Err: err}; return
			}
			started := false
			var streamErr error
			for ev := range ch {
				if ev.Type == EventError {
					streamErr = ev.Err
					break
				}
				started = true
				out <- ev
			}
			if streamErr == nil { return }
			if started || attempt >= policy.MaxAttempts || !retryable(streamErr) { out <- Event{Type: EventError, Err: streamErr}; return }
			if err := sleepBackoff(ctx, policy, attempt); err != nil { out <- Event{Type: EventError, Err: err}; return }
		}
	}()
	return out, nil
}

// StreamTurn atomically commits streamed model output only after EventDone. A failed stream restores the prior history.
func (c *RouterClient) StreamTurn(ctx context.Context, session *Session, user Turn, req Request) (<-chan Event, error) {
	before := session.History(); session.Append(user); req.Messages = session.History()
	ch, err := c.Stream(ctx, session, req); if err != nil { session.ReplaceHistory(before); return nil, err }
	out := make(chan Event, 16)
	go func() {
		defer close(out)
		var text []ContentPart; var calls []ToolCall
		for ev := range ch {
			out <- ev
			if ev.Type == EventText && ev.Text != "" { text = append(text, ContentPart{Type: ContentText, Text: ev.Text}) }
			if ev.Type == EventToolCall && ev.ToolCall != nil { calls = append(calls, *ev.ToolCall) }
			if ev.Type == EventDone { if len(text) > 0 { session.Append(Turn{Role: RoleModel, Content: text}) }; for _, call := range calls { call := call; session.Append(Turn{Role: RoleToolCall, ToolCall: &call}) }; return }
			if ev.Type == EventError { session.ReplaceHistory(before); return }
		}
		session.ReplaceHistory(before)
	}()
	return out, nil
}
