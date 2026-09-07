package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

type Config struct {
	DiscordToken   string `json:"discord_token"`
	DiscordOwnerID string `json:"discord_owner_id"`
	DiscordEnabled bool   `json:"discord_enabled"`
	CLIEnabled     bool   `json:"cli_enabled"`
}

const DefaultConfigPath = ".config/input.json"

func LoadConfig(path string) (Config, error) {
	if path == "" {
		path = DefaultConfigPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("transport: read input config %q: %w", path, err)
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("transport: decode input config %q: %w", path, err)
	}
	if config.DiscordToken != "" && config.DiscordOwnerID == "" {
		return Config{}, errors.New("transport: discord_owner_id is required when Discord is enabled")
	}
	if config.DiscordToken != "" {
		config.DiscordEnabled = true
	}
	return config, nil
}
