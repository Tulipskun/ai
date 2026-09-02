package transport

import "testing"

func TestLoadConfigRequiresDiscordToken(t *testing.T) {
	t.Setenv("DISCORD_BOT_TOKEN", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig() accepted an empty Discord token")
	}
}

func TestLoadConfigReadsDiscordToken(t *testing.T) {
	t.Setenv("DISCORD_BOT_TOKEN", "test-token")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.DiscordToken != "test-token" {
		t.Fatalf("DiscordToken = %q", config.DiscordToken)
	}
}
