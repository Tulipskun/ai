package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Tulipskun/ai/sdk"
	"github.com/Tulipskun/ai/sdk/providers/anthropic"
	"github.com/Tulipskun/ai/sdk/providers/gemini"
	"github.com/Tulipskun/ai/sdk/providers/openai"
)

type ProviderManager struct {
	mu     sync.Mutex
	path   string
	rt     *Runtime
	config ProviderFileConfig
}

func NewProviderManager(path string, rt *Runtime, config ProviderFileConfig) *ProviderManager {
	if path == "" {
		path = DefaultProviderConfigPath
	}
	return &ProviderManager{path: path, rt: rt, config: config}
}

func (m *ProviderManager) Adapters() []sdk.AdapterID {
	return []sdk.AdapterID{sdk.AdapterOpenAI, sdk.AdapterAnthropic, sdk.AdapterGemini}
}

func (m *ProviderManager) Upsert(ctx context.Context, name, adapter, endpoint, apiKey string) error {
	if m == nil || m.rt == nil || m.rt.Router == nil || m.rt.Client == nil {
		return errors.New("runtime: provider manager is not initialized")
	}
	name = strings.TrimSpace(name)
	adapter = strings.ToLower(strings.TrimSpace(adapter))
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	apiKey = strings.TrimSpace(apiKey)
	if name == "" {
		return errors.New("provider name is required")
	}
	if endpoint == "" {
		return errors.New("provider URL is required")
	}
	if apiKey == "" {
		return errors.New("provider API key is required")
	}
	if _, err := adapterForProvider(name, adapter); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	file := m.config
	index := -1
	for i := range file.Providers {
		if strings.EqualFold(file.Providers[i].Name, name) {
			index = i
			break
		}
	}
	entry := ProviderFile{Name: name, Adapter: adapter, HTTPEndpoint: endpoint, APIKeys: []string{apiKey}}
	if index >= 0 {
		file.Providers[index] = entry
	} else {
		file.Providers = append(file.Providers, entry)
	}
	configs, err := file.ProviderConfigs()
	if err != nil {
		return err
	}
	var selected sdk.ProviderConfig
	found := false
	for _, config := range configs {
		if string(config.ID) == name {
			selected = config
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("provider %q was not converted to an SDK config", name)
	}

	if err := m.persist(file); err != nil {
		return err
	}

	m.rt.Router.RegisterProvider(selected)
	if err := m.ensureAdapter(selected.Adapter); err != nil {
		return err
	}
	m.rt.ProviderConfigs = configs
	m.rt.Providers = providerIDs(configs)
	if err := m.rt.RefreshProvider(ctx, selected.ID); err != nil {
		return fmt.Errorf("provider %q saved but model discovery failed: %w", name, err)
	}
	m.config = file
	return nil
}

func (m *ProviderManager) ensureAdapter(id sdk.AdapterID) error {
	if _, ok := m.rt.Client.Adapters[id]; ok {
		return nil
	}
	switch id {
	case sdk.AdapterOpenAI:
		m.rt.Client.RegisterAdapter(id, openai.New(""))
	case sdk.AdapterAnthropic:
		m.rt.Client.RegisterAdapter(id, anthropic.New(""))
	case sdk.AdapterGemini:
		m.rt.Client.RegisterAdapter(id, gemini.New(""))
	default:
		return fmt.Errorf("runtime: unsupported adapter %q", id)
	}
	return nil
}

func (m *ProviderManager) persist(config ProviderFileConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("runtime: encode provider config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return fmt.Errorf("runtime: create provider config directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(m.path), ".provider-*.json")
	if err != nil {
		return fmt.Errorf("runtime: create provider config temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("runtime: chmod provider config temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("runtime: write provider config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("runtime: close provider config temp file: %w", err)
	}
	if err := os.Rename(tmpName, m.path); err != nil {
		return fmt.Errorf("runtime: replace provider config: %w", err)
	}
	return nil
}

func providerIDs(configs []sdk.ProviderConfig) []sdk.ProviderID {
	ids := make([]sdk.ProviderID, 0, len(configs))
	for _, config := range configs {
		ids = append(ids, config.ID)
	}
	return ids
}
