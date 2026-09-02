package sdk

import "context"

type KeyedProvider interface {
	Provider
	WithAPIKey(string) Provider
}

type RouterClient struct {
	Router   *Router
	Adapters map[AdapterID]Provider
}

func NewRouterClient(router *Router) *RouterClient {
	return &RouterClient{Router: router, Adapters: make(map[AdapterID]Provider)}
}

func (c *RouterClient) RegisterAdapter(id AdapterID, p Provider) {
	c.Adapters[id] = p
}

func (c *RouterClient) providerFor(session *Session) (Provider, ModelRoute, error) {
	route, err := c.Router.Resolve(session.config.Provider, session.config.Model)
	if err != nil {
		return nil, ModelRoute{}, err
	}
	p, ok := c.Adapters[route.Adapter]
	if !ok {
		return nil, ModelRoute{}, &RouteError{Provider: route.Provider, Model: route.Model, Adapter: route.Adapter}
	}
	if kp, ok := p.(KeyedProvider); ok {
		key, err := session.APIKey()
		if err != nil {
			return nil, ModelRoute{}, err
		}
		p = kp.WithAPIKey(key)
	}
	return p, route, nil
}

type RouteError struct {
	Provider ProviderID
	Model    string
	Adapter  AdapterID
}

func (e *RouteError) Error() string {
	return "sdk: adapter not registered for provider=" + string(e.Provider) + " model=" + e.Model + " adapter=" + string(e.Adapter)
}

func (c *RouterClient) Generate(ctx context.Context, session *Session, req Request) (Response, error) {
	p, route, err := c.providerFor(session)
	if err != nil {
		return Response{}, err
	}
	req.Provider = route.Provider
	req.Model = route.Model
	resp, err := p.Generate(ctx, req)
	if err == nil {
		resp.Provider = string(route.Provider)
		resp.Model = route.Model
	}
	return resp, err
}

func (c *RouterClient) Stream(ctx context.Context, session *Session, req Request) (<-chan Event, error) {
	p, route, err := c.providerFor(session)
	if err != nil {
		return nil, err
	}
	req.Provider = route.Provider
	req.Model = route.Model
	return p.Stream(ctx, req)
}
