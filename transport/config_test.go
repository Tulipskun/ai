package transport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigMissing(t *testing.T) {
	config, err := LoadConfig(filepath.Join(t.TempDir(), "entry.json"))
	if err != nil { t.Fatal(err) }
	if config.Discord.Enabled || config.CLI.Enabled { t.Fatalf("unexpected config: %+v", config) }
}

func TestLoadConfigReadsDiscordAndCLI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entry.json")
	data := []byte(`{"discord":{"token":"test-token","owner_id":"123456789","enabled":true},"cli":{"enabled":true}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil { t.Fatal(err) }
	config, err := LoadConfig(path)
	if err != nil { t.Fatal(err) }
	if !config.Discord.Enabled || config.Discord.Token != "test-token" || config.Discord.OwnerID != "123456789" || !config.CLI.Enabled { t.Fatalf("unexpected config: %+v", config) }
}

func TestLoadConfigRequiresDiscordOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entry.json")
	data := []byte(`{"discord":{"token":"test-token","enabled":true}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil { t.Fatal(err) }
	if _, err := LoadConfig(path); err == nil { t.Fatal("expected owner id validation error") }
}
