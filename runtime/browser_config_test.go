package runtime

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadBrowserConfigDefaults(t *testing.T) {
	cfg, err := LoadBrowserConfig(filepath.Join(t.TempDir(), "browser.json"))
	if err != nil { t.Fatal(err) }
	if cfg.Enabled || cfg.Browser != "auto" || cfg.Profile != ".data/browser/profile" || cfg.Headless || cfg.AllowPrivate {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if cfg.IdleTimeout != 30*time.Minute || cfg.NavigationTimeout != 30*time.Second || cfg.ActionTimeout != 10*time.Second || cfg.SnapshotTimeout != 10*time.Second {
		t.Fatalf("unexpected durations: %#v", cfg)
	}
}

func TestLoadBrowserConfigFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "browser.json")
	data := []byte(`{"enabled":true,"browser":"chrome","profile":"browser-profile","headless":false,"idle_timeout":"5m","allow_private":true,"navigation_timeout":"20s","action_timeout":"7s","snapshot_timeout":"8s"}`)
	if err := os.WriteFile(path, data, 0o600); err != nil { t.Fatal(err) }
	cfg, err := LoadBrowserConfig(path)
	if err != nil { t.Fatal(err) }
	if !cfg.Enabled || cfg.Browser != "chrome" || cfg.Profile != "browser-profile" || cfg.Headless || !cfg.AllowPrivate || cfg.IdleTimeout != 5*time.Minute || cfg.NavigationTimeout != 20*time.Second {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestLoadBrowserConfigRejectsUnknownBrowser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "browser.json")
	if err := os.WriteFile(path, []byte(`{"browser":"firefox"}`), 0o600); err != nil { t.Fatal(err) }
	if _, err := LoadBrowserConfig(path); err == nil { t.Fatal("expected invalid browser error") }
}
