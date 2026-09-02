package transport

import (
	"os"
	"strings"
)

type Config struct {
	DiscordToken   string
	DiscordEnabled bool
}

func LoadConfig() (Config, error) {
	token := strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN"))
	return Config{DiscordToken: token, DiscordEnabled: token != ""}, nil
}
