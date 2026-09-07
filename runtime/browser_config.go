package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type BrowserConfig struct {
	Enabled           bool          `json:"enabled"`
	Host              string        `json:"host"`
	Port              int           `json:"port"`
	NodeCommand       string        `json:"node_command"`
	WorkerPath        string        `json:"worker_path"`
	WorkerDir         string        `json:"worker_dir"`
	Headless          bool          `json:"headless"`
	IdleTimeout       time.Duration `json:"idle_timeout"`
	AllowPrivate      bool          `json:"allow_private"`
	NavigationTimeout time.Duration `json:"navigation_timeout"`
	ActionTimeout     time.Duration `json:"action_timeout"`
	SnapshotTimeout   time.Duration `json:"snapshot_timeout"`
	RPCStartupTimeout time.Duration `json:"rpc_startup_timeout"`
}

const DefaultBrowserConfigPath = ".config/browser.json"

func defaultBrowserConfig() BrowserConfig {
	return BrowserConfig{
		Host: "127.0.0.1", NodeCommand: "node", WorkerPath: "browser/server.mjs", WorkerDir: ".",
		Headless: true, IdleTimeout: 30 * time.Minute, NavigationTimeout: 30 * time.Second,
		ActionTimeout: 10 * time.Second, SnapshotTimeout: 10 * time.Second, RPCStartupTimeout: 30 * time.Second,
	}
}

func LoadBrowserConfig(path string) (BrowserConfig, error) {
	if path == "" { path = DefaultBrowserConfigPath }
	cfg := defaultBrowserConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) { return cfg, nil }
		return BrowserConfig{}, fmt.Errorf("browser: read config %q: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return BrowserConfig{}, fmt.Errorf("browser: decode config %q: %w", path, err)
	}
	if cfg.Host == "" || cfg.NodeCommand == "" || cfg.WorkerPath == "" || cfg.WorkerDir == "" {
		return BrowserConfig{}, fmt.Errorf("browser configuration contains an empty required value")
	}
	if cfg.Port < 0 || cfg.Port > 65535 { return BrowserConfig{}, fmt.Errorf("browser port must be between 0 and 65535") }
	return cfg, nil
}

func ParseBrowserDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" { return 0, fmt.Errorf("browser duration is required") }
	return time.ParseDuration(value)
}
