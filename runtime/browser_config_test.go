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
	if cfg.Enabled || cfg.Mode != "managed" || cfg.Browser != "auto" || cfg.Profile != "data/browser/profile" || cfg.Headless || cfg.AllowPrivate {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if cfg.IdleTimeout != 30*time.Minute || cfg.NavigationTimeout != 30*time.Second || cfg.ActionTimeout != 10*time.Second || cfg.SnapshotTimeout != 10*time.Second { t.Fatalf("unexpected durations: %#v", cfg) }
}

func TestLoadBrowserConfigFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "browser.json")
	data := []byte(`{"enabled":true,"mode":"attach","browser":"chrome","profile":"browser-profile","cdp_endpoint":"http://127.0.0.1:9222","headless":false,"idle_timeout":"5m","allow_private":true,"navigation_timeout":"20s","action_timeout":"7s","snapshot_timeout":"8s"}`)
	if err := os.WriteFile(path, data, 0o600); err != nil { t.Fatal(err) }
	cfg, err := LoadBrowserConfig(path)
	if err != nil { t.Fatal(err) }
	if !cfg.Enabled || cfg.Mode != "attach" || cfg.Browser != "chrome" || cfg.Profile != "browser-profile" || cfg.CDPEndpoint != "http://127.0.0.1:9222" || cfg.Headless || !cfg.AllowPrivate || cfg.IdleTimeout != 5*time.Minute || cfg.NavigationTimeout != 20*time.Second || cfg.ActionTimeout != 7*time.Second || cfg.SnapshotTimeout != 8*time.Second { t.Fatalf("unexpected config: %#v", cfg) }
}

func TestPartialBrowserConfigKeepsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "browser.json")
	if err := os.WriteFile(path, []byte(`{"enabled":true}`), 0o600); err != nil { t.Fatal(err) }
	cfg, err := LoadBrowserConfig(path)
	if err != nil { t.Fatal(err) }
	if !cfg.Enabled || cfg.Mode != "managed" || cfg.Browser != "auto" || cfg.Profile != "data/browser/profile" || cfg.IdleTimeout != 30*time.Minute || cfg.NavigationTimeout != 30*time.Second || cfg.ActionTimeout != 10*time.Second || cfg.SnapshotTimeout != 10*time.Second { t.Fatalf("partial config lost defaults: %#v", cfg) }
}

func TestLoadBrowserConfigRejectsUnknownBrowser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "browser.json")
	if err := os.WriteFile(path, []byte(`{"browser":"safari"}`), 0o600); err != nil { t.Fatal(err) }
	if _, err := LoadBrowserConfig(path); err == nil { t.Fatal("expected invalid browser error") }
}

func TestLoadBrowserConfigRequiresEndpointForAttach(t *testing.T) {
	path := filepath.Join(t.TempDir(), "browser.json")
	if err := os.WriteFile(path, []byte(`{"mode":"attach"}`), 0o600); err != nil { t.Fatal(err) }
	if _, err := LoadBrowserConfig(path); err == nil { t.Fatal("expected cdp endpoint error") }
}

func TestSaveBrowserConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "browser.json")
	cfg, err := LoadBrowserConfig(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil { t.Fatal(err) }
	cfg.Enabled = true
	cfg.Mode = "attach"
	cfg.Browser = "chromium"
	cfg.CDPEndpoint = "http://127.0.0.1:9222"
	cfg.Headless = true
	cfg.AllowPrivate = true
	cfg.Display = ":1"
	if err := SaveBrowserConfig(path, cfg); err != nil { t.Fatal(err) }
	got, err := LoadBrowserConfig(path)
	if err != nil { t.Fatal(err) }
	if !got.Enabled || got.Mode != "attach" || got.Browser != "chromium" || got.CDPEndpoint != "http://127.0.0.1:9222" || !got.Headless || !got.AllowPrivate || got.Display != ":1" { t.Fatalf("round trip mismatch: %#v", got) }
	if got.IdleTimeout != cfg.IdleTimeout || got.NavigationTimeout != cfg.NavigationTimeout || got.ActionTimeout != cfg.ActionTimeout || got.SnapshotTimeout != cfg.SnapshotTimeout { t.Fatalf("durations mismatch: %#v", got) }
}
