package transport

import (
	"errors"
	"os"
	"strings"
)

type Config struct {
	DiscordToken     string
	DiscordOwnerID   string
	DiscordEnabled   bool
}

func LoadConfig() (Config, error) {
	token := strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN"))
	ownerID := strings.TrimSpace(os.Getenv("DISCORD_OWNER_ID"))
	if token != "" && ownerID == "" {
		return Config{}, errors.New("transport: DISCORD_OWNER_ID is required when Discord is enabled")
	}
	return Config{DiscordToken: token, DiscordOwnerID: ownerID, DiscordEnabled: token != ""}, nil
}
