package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProviderFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "provider.json")
	data := []byte(`{"providers":[{"name":"openrouter","http_endpoint":"https://openrouter.ai/api/v1/","api_keys":[" key-1 ","key-2"]}]}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadProviderFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Providers) != 1 {
		t.Fatalf("providers=%d", len(config.Providers))
	}
	p := config.Providers[0]
	if p.Name != "openrouter" || p.HTTPEndpoint != "https://openrouter.ai/api/v1" {
		t.Fatalf("provider=%+v", p)
	}
	if len(p.APIKeys) != 2 || p.APIKeys[0] != "key-1" || p.APIKeys[1] != "key-2" {
		t.Fatalf("keys=%v", p.APIKeys)
	}
}

func TestProviderConfigsInferAdapters(t *testing.T) {
	config := ProviderFileConfig{Providers: []ProviderFile{
		{Name: "openrouter", HTTPEndpoint: "https://openrouter.ai/api/v1", APIKeys: []string{"key"}},
		{Name: "opencode", HTTPEndpoint: "https://example.invalid/v1", APIKeys: []string{"key"}},
	}}
	providers, err := config.ProviderConfigs()
	if err != nil {
		t.Fatal(err)
	}
	if providers[0].Adapter != "openai" || providers[1].Adapter != "anthropic" {
		t.Fatalf("adapters=%v,%v", providers[0].Adapter, providers[1].Adapter)
	}
}

func TestProviderConfigsUseExplicitAdapter(t *testing.T) {
	config := ProviderFileConfig{Providers: []ProviderFile{
		{Name: "B.ai", Adapter: "openai", HTTPEndpoint: "https://api.b.ai/v1", APIKeys: []string{"key"}},
	}}
	providers, err := config.ProviderConfigs()
	if err != nil {
		t.Fatal(err)
	}
	if providers[0].Adapter != "openai" {
		t.Fatalf("adapter=%v", providers[0].Adapter)
	}
}

func TestProviderConfigsRejectUnknownExplicitAdapter(t *testing.T) {
	config := ProviderFileConfig{Providers: []ProviderFile{
		{Name: "custom", Adapter: "unknown", HTTPEndpoint: "https://example.invalid/v1", APIKeys: []string{"key"}},
	}}
	if _, err := config.ProviderConfigs(); err == nil {
		t.Fatal("expected unknown adapter error")
	}
}

func TestLoadProviderFileRejectsMissingKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "provider.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"openrouter","http_endpoint":"https://example.invalid/v1","api_keys":[]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProviderFile(path); err == nil {
		t.Fatal("expected missing key error")
	}
}
