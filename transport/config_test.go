package transport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigMissing(t *testing.T) {
	config, err := LoadConfig(filepath.Join(t.TempDir(), "entry.json"))
	if err != nil {
		t.Fatal(err)
	}
	if config.Mobile.Enabled {
		t.Fatalf("unexpected config: %+v", config)
	}
}

func TestLoadConfigReadsMobile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entry.json")
	data := []byte(`{"mobile":{"enabled":true,"worker_base":"https://aixodia.example.workers.dev","listen":"127.0.0.1:18789","tunnel":true,"sync_config":true,"sync_sessions":true}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !config.Mobile.Enabled || config.Mobile.WorkerBase == "" || !config.Mobile.Tunnel ||
		!config.Mobile.SyncConfig || !config.Mobile.SyncSessions {
		t.Fatalf("unexpected config: %+v", config)
	}
}

func TestLoadConfigRequiresWorkerBase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entry.json")
	if err := os.WriteFile(path, []byte(`{"mobile":{"enabled":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected a worker_base validation error")
	}
	if !strings.Contains(err.Error(), "worker_base") {
		t.Fatalf("error = %v, want it to name worker_base", err)
	}
}

func TestSaveConfigRoundTripsMobileOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "entry.json")
	want := Config{Mobile: MobileConfig{Enabled: true, WorkerBase: "https://w.example", Tunnel: true}}
	if err := SaveConfig(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mobile != want.Mobile {
		t.Fatalf("round trip changed the config: %+v", got.Mobile)
	}
	// No credential may be written into the entry config (CON-012).
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "token") {
		t.Fatalf("entry config mentions a token: %s", raw)
	}
}
