package transport

import "testing"

func TestLoadConfigDisablesDiscordWithoutToken(t *testing.T) {
	t.Setenv("DISCORD_BOT_TOKEN", "")
	t.Setenv("DISCORD_OWNER_ID", "")
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
	t.Setenv("DISCORD_OWNER_ID", "123456789")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.DiscordEnabled || config.DiscordToken != "test-token" || config.DiscordOwnerID != "123456789" {
		t.Fatalf("unexpected config: %+v", config)
	}
}
