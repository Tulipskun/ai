package runtime

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Tulipskun/ai/sdk"
	"github.com/Tulipskun/ai/sdk/providers/anthropic"
	"github.com/Tulipskun/ai/sdk/providers/gemini"
	"github.com/Tulipskun/ai/sdk/providers/openai"
	"github.com/Tulipskun/ai/tools"
)

type Runtime struct {
	Router          *sdk.Router
	Client          *sdk.RouterClient
	Providers       []sdk.ProviderID
	ProviderConfigs []sdk.ProviderConfig
	Browser         *tools.BrowserClient
}

func Load(path string) (*Runtime, error) {
	file, err := LoadProviderFile(path)
	if err != nil { return nil, err }
	configs, err := file.ProviderConfigs()
	if err != nil { return nil, err }
	router := sdk.NewRouter()
	client := sdk.NewRouterClient(router)
	providers := make([]sdk.ProviderID, 0, len(configs))
	registeredAdapters := make(map[sdk.AdapterID]bool)
	for _, config := range configs {
		router.RegisterProvider(config)
		providers = append(providers, config.ID)
		if registeredAdapters[config.Adapter] { continue }
		switch config.Adapter {
		case sdk.AdapterOpenAI: client.RegisterAdapter(config.Adapter, openai.New(""))
		case sdk.AdapterAnthropic: client.RegisterAdapter(config.Adapter, anthropic.New(""))
		case sdk.AdapterGemini: client.RegisterAdapter(config.Adapter, gemini.New(""))
		default: return nil, fmt.Errorf("runtime: unsupported adapter %q", config.Adapter)
		}
		registeredAdapters[config.Adapter] = true
	}
	return &Runtime{Router: router, Client: client, Providers: providers, ProviderConfigs: configs}, nil
}

func (r *Runtime) StartBrowser(ctx context.Context, cfg BrowserConfig, stateRoot string) error {
	if r == nil { return fmt.Errorf("runtime: runtime is not initialized") }
	if !cfg.Enabled { return nil }
	if r.Browser != nil && r.Browser.Ready() { return nil }
	profile := cfg.Profile
	if !filepath.IsAbs(profile) { profile = filepath.Join(stateRoot, profile) }
	client := tools.NewBrowserClient(tools.BrowserClientConfig{Browser:cfg.Browser,Profile:profile,Headless:cfg.Headless,AllowPrivate:cfg.AllowPrivate,IdleTimeout:cfg.IdleTimeout,NavigationTimeout:cfg.NavigationTimeout,ActionTimeout:cfg.ActionTimeout,SnapshotTimeout:cfg.SnapshotTimeout})
	if cfg.Mode == "attach" {
		if err := client.Attach(ctx, cfg.CDPEndpoint); err != nil { return err }
	} else if err := client.Start(ctx); err != nil { return err }
	r.Browser = client
	return nil
}

func (r *Runtime) CloseBrowser() error { if r == nil || r.Browser == nil { return nil }; err := r.Browser.Close(); r.Browser = nil; return err }
func (r *Runtime) RefreshModels(ctx context.Context) error { if r == nil || r.Router == nil || r.Client == nil { return fmt.Errorf("runtime: runtime is not initialized") }; for _, provider := range r.Providers { if err := r.Client.RefreshModels(ctx, provider); err != nil { return fmt.Errorf("runtime: refresh provider %q: %w", provider, err) } }; return nil }
func (r *Runtime) RefreshProvider(ctx context.Context, provider sdk.ProviderID) error { if r == nil || r.Client == nil { return fmt.Errorf("runtime: runtime is not initialized") }; return r.Client.RefreshModels(ctx, provider) }
