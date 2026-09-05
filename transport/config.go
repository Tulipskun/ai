package transport

import (
	"errors"
	"os"
	"strings"
)

type Config struct {
	DiscordToken   string
	DiscordOwnerID string
	DiscordEnabled bool
	CLIEnabled     bool
}

func LoadConfig() (Config, error) {
	token := strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN"))
	ownerID := strings.TrimSpace(os.Getenv("DISCORD_OWNER_ID"))
	if token != "" && ownerID == "" {
		return Config{}, errors.New("transport: DISCORD_OWNER_ID is required when Discord is enabled")
	}
	cliEnabled := strings.EqualFold(strings.TrimSpace(os.Getenv("AI_CLI_ENABLED")), "true")
	return Config{DiscordToken: token, DiscordOwnerID: ownerID, DiscordEnabled: token != "", CLIEnabled: cliEnabled}, nil
}
