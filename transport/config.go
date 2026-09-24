package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// MobileConfig points the daemon at the AIxodia Worker and the local listener a
// Cloudflare quick tunnel publishes (REQ-046, REQ-047). It carries no
// credential: the D1 token arrives in the phone's Authorization header and is
// held in memory only (CON-012).
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

// Config is the whole entry config: one gateway, mobile over tunnel. The
// Discord and CLI blocks were removed with their transports (CHANGE-059).
type Config struct {
	Mobile MobileConfig `json:"mobile"`
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
