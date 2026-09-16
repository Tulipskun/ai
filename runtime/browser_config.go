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
	Enabled              bool          `json:"enabled"`
	Mode                 string        `json:"mode"`
	Headless             bool          `json:"headless"`
	Browser              string        `json:"browser"`
	Profile              string        `json:"profile"`
	CDPEndpoint          string        `json:"cdp_endpoint"`
	AllowPrivate         bool          `json:"allow_private"`
	Display              string        `json:"display"`
	IdleTimeout          time.Duration `json:"-"`
	NavigationTimeout    time.Duration `json:"-"`
	ActionTimeout        time.Duration `json:"-"`
	SnapshotTimeout      time.Duration `json:"-"`
}

const DefaultBrowserConfigPath = "config/browser.json"

func defaultBrowserConfig() BrowserConfig {
	return BrowserConfig{
		Browser:           "auto",
		Mode:              "managed",
		Profile:           "data/browser/profile",
		Headless:          false,
		IdleTimeout:       30 * time.Minute,
		NavigationTimeout: 30 * time.Second,
		ActionTimeout:     10 * time.Second,
		SnapshotTimeout:   10 * time.Second,
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
	if err := json.Unmarshal(data, &cfg); err != nil { return BrowserConfig{}, fmt.Errorf("browser: decode config %q: %w", path, err) }
	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode)); if cfg.Mode == "" { cfg.Mode = "managed" }
	cfg.Browser = strings.ToLower(strings.TrimSpace(cfg.Browser)); if cfg.Browser == "" { cfg.Browser = "auto" }
	if cfg.Mode != "managed" && cfg.Mode != "attach" { return BrowserConfig{}, fmt.Errorf("browser mode must be one of managed, attach") }
	switch cfg.Browser { case "auto", "chrome", "chromium", "edge", "firefox": default: return BrowserConfig{}, fmt.Errorf("browser must be one of auto, chrome, chromium, edge, firefox") }
	if cfg.Mode == "managed" && strings.TrimSpace(cfg.Profile) == "" { return BrowserConfig{}, fmt.Errorf("browser profile is required") }
	if cfg.Mode == "attach" && strings.TrimSpace(cfg.CDPEndpoint) == "" { return BrowserConfig{}, fmt.Errorf("cdp_endpoint is required in attach mode") }
	return cfg, nil
}

func SaveBrowserConfig(path string, cfg BrowserConfig) error {
	if path == "" { path = DefaultBrowserConfigPath }
	out := map[string]any{
		"enabled": cfg.Enabled, "mode": cfg.Mode, "headless": cfg.Headless,
		"browser": cfg.Browser, "profile": cfg.Profile, "cdp_endpoint": cfg.CDPEndpoint, "display": cfg.Display,
		"allow_private": cfg.AllowPrivate, "idle_timeout": cfg.IdleTimeout.String(),
		"navigation_timeout": cfg.NavigationTimeout.String(), "action_timeout": cfg.ActionTimeout.String(),
		"snapshot_timeout": cfg.SnapshotTimeout.String(),
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil { return fmt.Errorf("browser: encode config: %w", err) }
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil { return fmt.Errorf("browser: create config directory: %w", err) }
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil { return fmt.Errorf("browser: write config %q: %w", path, err) }
	return nil
}

func dirOf(path string) string { i := strings.LastIndex(path, "/"); if i < 0 { return "." }; if i == 0 { return "/" }; return path[:i] }

func (c *BrowserConfig) UnmarshalJSON(data []byte) error {
	defaults := defaultBrowserConfig()
	type raw struct {
		Enabled *bool `json:"enabled"`; Mode *string `json:"mode"`; Headless *bool `json:"headless"`; Browser *string `json:"browser"`; Profile *string `json:"profile"`; CDPEndpoint *string `json:"cdp_endpoint"`; AllowPrivate *bool `json:"allow_private"`; Display *string `json:"display"`
		IdleTimeout string `json:"idle_timeout"`; NavigationTimeout string `json:"navigation_timeout"`; ActionTimeout string `json:"action_timeout"`; SnapshotTimeout string `json:"snapshot_timeout"`
	}
	var r raw; if err := json.Unmarshal(data, &r); err != nil { return err }
	*c = defaults
	if r.Enabled != nil { c.Enabled = *r.Enabled }
	if r.Mode != nil { c.Mode = *r.Mode }
	if r.Headless != nil { c.Headless = *r.Headless }
	if r.Browser != nil { c.Browser = *r.Browser }
	if r.Profile != nil { c.Profile = *r.Profile }
	if r.CDPEndpoint != nil { c.CDPEndpoint = *r.CDPEndpoint }
	if r.AllowPrivate != nil { c.AllowPrivate = *r.AllowPrivate }
	if r.Display != nil { c.Display = strings.TrimSpace(*r.Display) }
	parse := func(name, value string, fallback time.Duration) (time.Duration, error) { if strings.TrimSpace(value)=="" { return fallback,nil }; d,err:=time.ParseDuration(value); if err!=nil{return 0,fmt.Errorf("%s: %w",name,err)};if d<=0{return 0,fmt.Errorf("%s must be positive",name)};return d,nil }
	var err error
	if c.IdleTimeout,err=parse("idle_timeout",r.IdleTimeout,defaults.IdleTimeout);err!=nil{return err}
	if c.NavigationTimeout,err=parse("navigation_timeout",r.NavigationTimeout,defaults.NavigationTimeout);err!=nil{return err}
	if c.ActionTimeout,err=parse("action_timeout",r.ActionTimeout,defaults.ActionTimeout);err!=nil{return err}
	if c.SnapshotTimeout,err=parse("snapshot_timeout",r.SnapshotTimeout,defaults.SnapshotTimeout);err!=nil{return err}
	return nil
}
