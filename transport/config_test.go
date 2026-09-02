package transport

import "testing"

func TestLoadConfigDisablesDiscordWithoutToken(t *testing.T) {
	t.Setenv("DISCORD_BOT_TOKEN", "")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.DiscordEnabled {
		t.Fatal("Discord is enabled without a token")
	}
}

func TestLoadConfigReadsDiscordToken(t *testing.T) {
	t.Setenv("DISCORD_BOT_TOKEN", "test-token")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.DiscordEnabled || config.DiscordToken != "test-token" {
		t.Fatalf("unexpected config: %+v", config)
	}
}
