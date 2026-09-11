package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type SystemConfig struct {
	SystemPrompt string `json:"system_prompt"`
}

const DefaultSystemConfigPath = "config/system.json"

func LoadSystemConfig(path string) (SystemConfig, error) {
	if path == "" { path = DefaultSystemConfigPath }
	var cfg SystemConfig
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) { return cfg, nil }
		return SystemConfig{}, fmt.Errorf("system: read config %q: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return SystemConfig{}, fmt.Errorf("system: decode config %q: %w", path, err)
	}
	cfg.SystemPrompt = strings.TrimSpace(cfg.SystemPrompt)
	return cfg, nil
}

func SaveSystemConfig(path string, cfg SystemConfig) error {
	if path == "" { path = DefaultSystemConfigPath }
	cfg.SystemPrompt = strings.TrimSpace(cfg.SystemPrompt)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil { return fmt.Errorf("system: encode config: %w", err) }
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil { return fmt.Errorf("system: create config directory: %w", err) }
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil { return fmt.Errorf("system: write config %q: %w", path, err) }
	return nil
}
