package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const DefaultConfigPath = ".config/config.json"

type Config struct {
	Providers []ProviderFile `json:"providers"`
	Discord   DiscordConfig  `json:"discord"`
	CLI       CLIConfig      `json:"cli"`
}

type DiscordConfig struct {
	Token   string `json:"token"`
	OwnerID string `json:"owner_id"`
}

type CLIConfig struct {
	Enabled bool `json:"enabled"`
}

func LoadConfig(path string) (Config, error) {
	if path == "" {
		path = DefaultConfigPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("runtime: read config %q: %w", path, err)
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("runtime: decode config %q: %w", path, err)
	}
	return config, nil
}

func (c Config) ProviderFileConfig() ProviderFileConfig {
	return ProviderFileConfig{Providers: c.Providers}
}

func (c Config) Save(path string) error {
	if path == "" {
		path = DefaultConfigPath
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("runtime: encode config: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("runtime: create config directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return fmt.Errorf("runtime: create config temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("runtime: chmod config temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("runtime: write config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("runtime: close config temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("runtime: replace config: %w", err)
	}
	return nil
}

func (c Config) ApplyEnvironment() Config {
	if value := strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN")); value != "" {
		c.Discord.Token = value
	}
	if value := strings.TrimSpace(os.Getenv("DISCORD_OWNER_ID")); value != "" {
		c.Discord.OwnerID = value
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("AI_CLI_ENABLED")), "true") {
		c.CLI.Enabled = true
	}
	return c
}
