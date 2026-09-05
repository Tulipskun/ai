package transport

import "testing"

func TestLoadConfigDisablesDiscordWithoutToken(t *testing.T) {
	t.Setenv("DISCORD_BOT_TOKEN", "")
	t.Setenv("DISCORD_OWNER_ID", "")
	t.Setenv("AI_CLI_ENABLED", "")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.DiscordEnabled || config.CLIEnabled {
		t.Fatal("no transport should be enabled")
	}
}

func TestLoadConfigReadsDiscordToken(t *testing.T) {
	t.Setenv("DISCORD_BOT_TOKEN", "test-token")
	t.Setenv("DISCORD_OWNER_ID", "123456789")
	t.Setenv("AI_CLI_ENABLED", "")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.DiscordEnabled || config.DiscordToken != "test-token" || config.DiscordOwnerID != "123456789" {
		t.Fatalf("unexpected config: %+v", config)
	}
}

func TestLoadConfigEnablesCLI(t *testing.T) {
	t.Setenv("DISCORD_BOT_TOKEN", "")
	t.Setenv("DISCORD_OWNER_ID", "")
	t.Setenv("AI_CLI_ENABLED", "true")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.CLIEnabled || config.DiscordEnabled {
		t.Fatalf("unexpected config: %+v", config)
	}
}
