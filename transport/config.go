package transport

import (
	"errors"
	"os"
	"strings"
)

type Config struct {
	DiscordToken string
}

func LoadConfig() (Config, error) {
	config := Config{DiscordToken: strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN"))}
	if config.DiscordToken == "" {
		return Config{}, errors.New("transport: DISCORD_BOT_TOKEN is required")
	}
	return config, nil
}
