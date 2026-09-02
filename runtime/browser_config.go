package runtime

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type BrowserConfig struct {
	Enabled              bool
	Host                 string
	Port                 int
	NodeCommand          string
	WorkerPath           string
	WorkerDir            string
	Headless             bool
	IdleTimeout          time.Duration
	AllowPrivate         bool
	NavigationTimeout    time.Duration
	ActionTimeout        time.Duration
	SnapshotTimeout      time.Duration
	RPCStartupTimeout    time.Duration
}

func LoadBrowserConfig() (BrowserConfig, error) {
	cfg := BrowserConfig{
		Enabled:           envBoolDefault("AI_BROWSER_ENABLED", false),
		Host:              envOrDefault("AI_BROWSER_HOST", "127.0.0.1"),
		Port:              envIntDefault("AI_BROWSER_PORT", 0),
		NodeCommand:       envOrDefault("AI_BROWSER_NODE", "node"),
		WorkerPath:        envOrDefault("AI_BROWSER_WORKER", "browser/server.mjs"),
		WorkerDir:         envOrDefault("AI_BROWSER_WORKER_DIR", "."),
		Headless:          envBoolDefault("AI_BROWSER_HEADLESS", true),
		IdleTimeout:       envDurationDefault("AI_BROWSER_IDLE_TIMEOUT", 30*time.Minute),
		AllowPrivate:      envBoolDefault("AI_BROWSER_ALLOW_PRIVATE", false),
		NavigationTimeout: envDurationDefault("AI_BROWSER_NAVIGATION_TIMEOUT", 30*time.Second),
		ActionTimeout:     envDurationDefault("AI_BROWSER_ACTION_TIMEOUT", 10*time.Second),
		SnapshotTimeout:   envDurationDefault("AI_BROWSER_SNAPSHOT_TIMEOUT", 10*time.Second),
		RPCStartupTimeout: envDurationDefault("AI_BROWSER_STARTUP_TIMEOUT", 30*time.Second),
	}
	if cfg.Host == "" || cfg.NodeCommand == "" || cfg.WorkerPath == "" || cfg.WorkerDir == "" {
		return BrowserConfig{}, fmt.Errorf("browser configuration contains an empty required value")
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		return BrowserConfig{}, fmt.Errorf("AI_BROWSER_PORT must be between 0 and 65535")
	}
	return cfg, nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envBoolDefault(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envIntDefault(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDurationDefault(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
