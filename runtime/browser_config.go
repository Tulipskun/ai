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
	Headless          bool          `json:"headless"`
	Browser           string        `json:"browser"`
	Profile           string        `json:"profile"`
	AllowPrivate      bool          `json:"allow_private"`
	IdleTimeout       time.Duration `json:"-"`
	NavigationTimeout time.Duration `json:"-"`
	ActionTimeout     time.Duration `json:"-"`
	SnapshotTimeout   time.Duration `json:"-"`
}

const DefaultBrowserConfigPath = ".config/browser.json"

func defaultBrowserConfig() BrowserConfig {
	return BrowserConfig{
		Browser: "auto",
		Profile: ".data/browser/profile",
		Headless: false,
		IdleTimeout: 30 * time.Minute,
		NavigationTimeout: 30 * time.Second,
		ActionTimeout: 10 * time.Second,
		SnapshotTimeout: 10 * time.Second,
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
	cfg.Browser = strings.ToLower(strings.TrimSpace(cfg.Browser))
	if cfg.Browser == "" { cfg.Browser = "auto" }
	switch cfg.Browser {
	case "auto", "chrome", "chromium", "edge":
	default:
		return BrowserConfig{}, fmt.Errorf("browser must be one of auto, chrome, chromium, edge")
	}
	if strings.TrimSpace(cfg.Profile) == "" { return BrowserConfig{}, fmt.Errorf("browser profile is required") }
	return cfg, nil
}

func (c *BrowserConfig) UnmarshalJSON(data []byte) error {
	type raw struct {
		Enabled bool `json:"enabled"`
		Headless bool `json:"headless"`
		Browser string `json:"browser"`
		Profile string `json:"profile"`
		AllowPrivate bool `json:"allow_private"`
		IdleTimeout string `json:"idle_timeout"`
		NavigationTimeout string `json:"navigation_timeout"`
		ActionTimeout string `json:"action_timeout"`
		SnapshotTimeout string `json:"snapshot_timeout"`
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
	*c = BrowserConfig{Enabled:r.Enabled, Headless:r.Headless, Browser:r.Browser, Profile:r.Profile, AllowPrivate:r.AllowPrivate, IdleTimeout:idle, NavigationTimeout:nav, ActionTimeout:action, SnapshotTimeout:snapshot}
	return nil
}
