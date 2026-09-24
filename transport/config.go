package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type DiscordConfig struct {
	Token   string `json:"token"`
	OwnerID string `json:"owner_id"`
	Enabled bool   `json:"enabled"`
}

type CLIConfig struct {
	Enabled bool `json:"enabled"`
}

// MobileConfig points the daemon at the AIxodia Worker and the local listener a
// Cloudflare quick tunnel publishes (REQ-046). It carries no credential: the
// D1 token arrives in the phone's Authorization header and is held in memory.
type MobileConfig struct {
	Enabled      bool   `json:"enabled"`
	WorkerBase   string `json:"worker_base"`
	Listen       string `json:"listen"`
	PublicListen string `json:"public_listen"`
	Tunnel       bool   `json:"tunnel"`
	Cloudflared  string `json:"cloudflared"`
	SyncConfig   bool   `json:"sync_config"`
	SyncSessions bool   `json:"sync_sessions"`
}

type Config struct {
	Discord DiscordConfig `json:"discord"`
	CLI     CLIConfig     `json:"cli"`
	Mobile  MobileConfig  `json:"mobile"`
}

const DefaultConfigPath = "config/entry.json"

func LoadConfig(path string) (Config, error) {
	if path == "" {
		path = DefaultConfigPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("transport: read entry config %q: %w", path, err)
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("transport: decode entry config %q: %w", path, err)
	}
	if config.Discord.Enabled && config.Discord.Token == "" {
		return Config{}, errors.New("transport: discord token is required when Discord is enabled")
	}
	if config.Discord.Enabled && config.Discord.OwnerID == "" {
		return Config{}, errors.New("transport: discord owner_id is required when Discord is enabled")
	}
	if config.Mobile.Enabled && config.Mobile.WorkerBase == "" {
		return Config{}, errors.New("transport: mobile worker_base is required when mobile is enabled")
	}
	return config, nil
}

func SaveConfig(path string, config Config) error {
	if path == "" {
		path = DefaultConfigPath
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("transport: encode entry config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("transport: create config directory: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("transport: write entry config %q: %w", path, err)
	}
	return nil
}
