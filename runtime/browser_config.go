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
	IdleTimeout       time.Duration `json:"-"`
	AllowPrivate      bool          `json:"allow_private"`
	NavigationTimeout time.Duration `json:"-"`
	ActionTimeout     time.Duration `json:"-"`
	SnapshotTimeout   time.Duration `json:"-"`
	RPCStartupTimeout time.Duration `json:"-"`
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

func (c *BrowserConfig) UnmarshalJSON(data []byte) error {
	type raw struct {
		Enabled bool `json:"enabled"`
		Host string `json:"host"`
		Port int `json:"port"`
		NodeCommand string `json:"node_command"`
		WorkerPath string `json:"worker_path"`
		WorkerDir string `json:"worker_dir"`
		Headless bool `json:"headless"`
		IdleTimeout string `json:"idle_timeout"`
		AllowPrivate bool `json:"allow_private"`
		NavigationTimeout string `json:"navigation_timeout"`
		ActionTimeout string `json:"action_timeout"`
		SnapshotTimeout string `json:"snapshot_timeout"`
		RPCStartupTimeout string `json:"rpc_startup_timeout"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil { return err }
	parse := func(name, value string, fallback time.Duration) (time.Duration, error) {
		if strings.TrimSpace(value) == "" { return fallback, nil }
		d, err := time.ParseDuration(value)
		if err != nil { return 0, fmt.Errorf("%s: %w", name, err) }
		if d <= 0 { return 0, fmt.Errorf("%s must be positive", name) }
		return d, nil
	}
	idle, err := parse("idle_timeout", r.IdleTimeout, 30*time.Minute); if err != nil { return err }
	nav, err := parse("navigation_timeout", r.NavigationTimeout, 30*time.Second); if err != nil { return err }
	action, err := parse("action_timeout", r.ActionTimeout, 10*time.Second); if err != nil { return err }
	snapshot, err := parse("snapshot_timeout", r.SnapshotTimeout, 10*time.Second); if err != nil { return err }
	rpc, err := parse("rpc_startup_timeout", r.RPCStartupTimeout, 30*time.Second); if err != nil { return err }
	*c = BrowserConfig{Enabled:r.Enabled, Host:r.Host, Port:r.Port, NodeCommand:r.NodeCommand, WorkerPath:r.WorkerPath, WorkerDir:r.WorkerDir, Headless:r.Headless, IdleTimeout:idle, AllowPrivate:r.AllowPrivate, NavigationTimeout:nav, ActionTimeout:action, SnapshotTimeout:snapshot, RPCStartupTimeout:rpc}
	return nil
}
