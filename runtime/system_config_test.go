package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSystemConfigMissingIsEmpty(t *testing.T) {
	cfg, err := LoadSystemConfig(filepath.Join(t.TempDir(), "system.json"))
	if err != nil { t.Fatal(err) }
	if cfg.SystemPrompt != "" { t.Fatalf("expected empty prompt, got %q", cfg.SystemPrompt) }
}

func TestSaveSystemConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "system.json")
	if err := SaveSystemConfig(path, SystemConfig{SystemPrompt: "  be helpful  "}); err != nil { t.Fatal(err) }
	got, err := LoadSystemConfig(path)
	if err != nil { t.Fatal(err) }
	if got.SystemPrompt != "be helpful" { t.Fatalf("round trip = %q", got.SystemPrompt) }
}

func TestLoadSystemConfigRejectsBadJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "system.json")
	if err := os.WriteFile(path, []byte("{oops"), 0o600); err != nil { t.Fatal(err) }
	if _, err := LoadSystemConfig(path); err == nil { t.Fatal("expected decode error") }
}
