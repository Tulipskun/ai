package runtime

import (
	"context"
	"fmt"

	"github.com/Tulipskun/ai/sdk"
	"github.com/Tulipskun/ai/sdk/providers/anthropic"
	"github.com/Tulipskun/ai/sdk/providers/gemini"
	"github.com/Tulipskun/ai/sdk/providers/openai"
)

type Runtime struct {
	Router *sdk.Router
	Client *sdk.RouterClient
}

func Load(path string) (*Runtime, error) {
	file, err := LoadProviderFile(path)
	if err != nil {
		return nil, err
	}
	configs, err := file.ProviderConfigs()
	if err != nil {
		return nil, err
	}

	router := sdk.NewRouter()
	client := sdk.NewRouterClient(router)
	for _, config := range configs {
		router.RegisterProvider(config)
		switch config.Adapter {
		case sdk.AdapterOpenAI:
			client.RegisterAdapter(config.Adapter, openai.New(""))
		case sdk.AdapterAnthropic:
			client.RegisterAdapter(config.Adapter, anthropic.New(""))
		case sdk.AdapterGemini:
			client.RegisterAdapter(config.Adapter, gemini.New(""))
		default:
			return nil, fmt.Errorf("runtime: unsupported adapter %q", config.Adapter)
		}
	}
	return &Runtime{Router: router, Client: client}, nil
}

func (r *Runtime) RefreshModels(ctx context.Context) error {
	if r == nil || r.Router == nil || r.Client == nil {
		return fmt.Errorf("runtime: runtime is not initialized")
	}
	providers := make([]sdk.ProviderID, 0)
	// The router exposes the registered provider configuration through Provider;
	// keep the runtime config source of truth external to the router.
	// Callers that need discovery should use RefreshProvider for a specific ID.
	_ = providers
	return nil
}

func (r *Runtime) RefreshProvider(ctx context.Context, provider sdk.ProviderID) error {
	if r == nil || r.Client == nil {
		return fmt.Errorf("runtime: runtime is not initialized")
	}
	return r.Client.RefreshModels(ctx, provider)
}
