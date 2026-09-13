package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Tulipskun/ai/sdk"
)

type SubAgentConfig struct {
	Enabled          bool                 `json:"enabled"`
	Provider         string               `json:"provider"`
	Model            string               `json:"model"`
	MaxOutputTokens  int                  `json:"max_output_tokens"`
	Temperature      *float64             `json:"temperature,omitempty"`
	ThinkingLevel    sdk.ThinkingLevel    `json:"thinking_level,omitempty"`
}

type SubAgentSystemConfig struct {
	SubAgent SubAgentConfig `json:"sub_agent"`
}

func normalizeSubAgentConfig(cfg SubAgentConfig) SubAgentConfig {
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.MaxOutputTokens < 0 {
		cfg.MaxOutputTokens = 0
	}
	return cfg
}

func LoadSubAgentConfig(path string) (SubAgentConfig, error) {
	if path == "" {
		path = DefaultSystemConfigPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SubAgentConfig{}, nil
		}
		return SubAgentConfig{}, fmt.Errorf("sub-agent: read config %q: %w", path, err)
	}
	var file SubAgentSystemConfig
	if err := json.Unmarshal(data, &file); err != nil {
		return SubAgentConfig{}, fmt.Errorf("sub-agent: decode config %q: %w", path, err)
	}
	return normalizeSubAgentConfig(file.SubAgent), nil
}

func SaveSubAgentConfig(path string, cfg SubAgentConfig) error {
	if path == "" {
		path = DefaultSystemConfigPath
	}
	cfg = normalizeSubAgentConfig(cfg)
	var file SubAgentSystemConfig
	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, &file)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("sub-agent: read config %q: %w", path, err)
	}
	file.SubAgent = cfg
	data, err = json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("sub-agent: encode config: %w", err)
	}
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		return fmt.Errorf("sub-agent: create config directory: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("sub-agent: write config %q: %w", path, err)
	}
	return nil
}
