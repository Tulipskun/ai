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
	if cfg.Enabled || cfg.Host != "127.0.0.1" || cfg.Port != 0 || cfg.NodeCommand != "node" || cfg.WorkerPath != "browser/server.mjs" || !cfg.Headless || cfg.AllowPrivate {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if cfg.IdleTimeout != 30*time.Minute || cfg.NavigationTimeout != 30*time.Second || cfg.ActionTimeout != 10*time.Second || cfg.SnapshotTimeout != 10*time.Second || cfg.RPCStartupTimeout != 30*time.Second {
		t.Fatalf("unexpected durations: %#v", cfg)
	}
}

func TestLoadBrowserConfigFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "browser.json")
	data := []byte(`{"enabled":true,"host":"127.0.0.1","port":12345,"node_command":"node","worker_path":"browser/server.mjs","worker_dir":".","headless":false,"idle_timeout":"5m","allow_private":true,"navigation_timeout":"20s","action_timeout":"7s","snapshot_timeout":"8s","rpc_startup_timeout":"15s"}`)
	if err := os.WriteFile(path, data, 0o600); err != nil { t.Fatal(err) }
	cfg, err := LoadBrowserConfig(path)
	if err != nil { t.Fatal(err) }
	if !cfg.Enabled || cfg.Port != 12345 || cfg.Headless || !cfg.AllowPrivate || cfg.IdleTimeout != 5*time.Minute || cfg.NavigationTimeout != 20*time.Second {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestLoadBrowserConfigRejectsInvalidPort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "browser.json")
	if err := os.WriteFile(path, []byte(`{"port":70000}`), 0o600); err != nil { t.Fatal(err) }
	if _, err := LoadBrowserConfig(path); err == nil {
		t.Fatal("expected invalid port error")
	}
}
