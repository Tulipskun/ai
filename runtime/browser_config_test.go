package runtime

import (
	"testing"
	"time"
)

func TestLoadBrowserConfigDefaults(t *testing.T) {
	for _, name := range []string{"AI_BROWSER_ENABLED", "AI_BROWSER_HOST", "AI_BROWSER_PORT", "AI_BROWSER_NODE", "AI_BROWSER_WORKER", "AI_BROWSER_WORKER_DIR", "AI_BROWSER_HEADLESS", "AI_BROWSER_IDLE_TIMEOUT", "AI_BROWSER_ALLOW_PRIVATE", "AI_BROWSER_NAVIGATION_TIMEOUT", "AI_BROWSER_ACTION_TIMEOUT", "AI_BROWSER_SNAPSHOT_TIMEOUT", "AI_BROWSER_STARTUP_TIMEOUT"} {
		t.Setenv(name, "")
	}
	cfg, err := LoadBrowserConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled || cfg.Host != "127.0.0.1" || cfg.Port != 0 || cfg.NodeCommand != "node" || cfg.WorkerPath != "browser/server.mjs" || cfg.Headless != true || cfg.AllowPrivate != false {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if cfg.IdleTimeout != 30*time.Minute || cfg.NavigationTimeout != 30*time.Second || cfg.ActionTimeout != 10*time.Second || cfg.SnapshotTimeout != 10*time.Second || cfg.RPCStartupTimeout != 30*time.Second {
		t.Fatalf("unexpected durations: %#v", cfg)
	}
}

func TestLoadBrowserConfigOverrides(t *testing.T) {
	t.Setenv("AI_BROWSER_ENABLED", "true")
	t.Setenv("AI_BROWSER_PORT", "12345")
	t.Setenv("AI_BROWSER_HEADLESS", "false")
	t.Setenv("AI_BROWSER_ALLOW_PRIVATE", "true")
	t.Setenv("AI_BROWSER_IDLE_TIMEOUT", "5m")
	cfg, err := LoadBrowserConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.Port != 12345 || cfg.Headless || !cfg.AllowPrivate || cfg.IdleTimeout != 5*time.Minute {
		t.Fatalf("unexpected overrides: %#v", cfg)
	}
}

func TestLoadBrowserConfigRejectsInvalidPort(t *testing.T) {
	t.Setenv("AI_BROWSER_PORT", "70000")
	if _, err := LoadBrowserConfig(); err == nil {
		t.Fatal("expected invalid port error")
	}
}
