package transport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigMissing(t *testing.T) {
	config, err := LoadConfig(filepath.Join(t.TempDir(), "input.json"))
	if err != nil {
		t.Fatal(err)
	}
	if config.DiscordEnabled || config.CLIEnabled {
		t.Fatalf("unexpected config: %+v", config)
	}
}

func TestLoadConfigReadsDiscordAndCLI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.json")
	data := []byte(`{"discord_token":"test-token","discord_owner_id":"123456789","cli_enabled":true}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !config.DiscordEnabled || config.DiscordToken != "test-token" || config.DiscordOwnerID != "123456789" || !config.CLIEnabled {
		t.Fatalf("unexpected config: %+v", config)
	}
}

func TestLoadConfigRequiresDiscordOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.json")
	data := []byte(`{"discord_token":"test-token"}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected owner id validation error")
	}
}
