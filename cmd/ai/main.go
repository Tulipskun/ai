package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Tulipskun/ai/runtime"
	"github.com/Tulipskun/ai/sdk"
	"github.com/Tulipskun/ai/transport"
	discordtransport "github.com/Tulipskun/ai/transport/discord"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	providerConfigPath := envOr("AI_PROVIDER_CONFIG", ".config/provider.json")
	providerName := strings.TrimSpace(os.Getenv("AI_PROVIDER"))
	model := strings.TrimSpace(os.Getenv("AI_MODEL"))
	if model == "" {
		return errors.New("AI_MODEL is required")
	}

	rt, err := runtime.Load(providerConfigPath)
	if err != nil {
		return err
	}
	if providerName == "" {
		if len(rt.Providers) != 1 {
			return errors.New("AI_PROVIDER is required when provider.json contains multiple providers")
		}
		providerName = string(rt.Providers[0])
	}
	provider := sdk.ProviderID(providerName)
	if err := rt.RefreshProvider(ctx, provider); err != nil {
		return err
	}
	if _, err := rt.Router.Resolve(provider, model); err != nil {
		return fmt.Errorf("validate model: %w", err)
	}

	providerConfig, err := rt.Router.Provider(provider)
	if err != nil {
		return err
	}
	if providerConfig.Keys == nil {
		return errors.New("selected provider has no API keys")
	}

	thinking := sdk.ThinkingLevel(strings.TrimSpace(os.Getenv("AI_THINKING_LEVEL")))
	if thinking == "" {
		thinking = sdk.ThinkingNone
	}
	temperature, err := envFloat("AI_TEMPERATURE")
	if err != nil {
		return err
	}
	maxOutputTokens, err := envInt("AI_MAX_OUTPUT_TOKENS")
	if err != nil {
		return err
	}

	sessionDB := envOr("AI_SESSION_DB", ".data/sessions.db")
	sessions := runtime.NewSessionManager(sessionDB, sdk.SessionConfig{
		Provider:      provider,
		Model:         model,
		KeyIndex:      0,
		ThinkingLevel: thinking,
		Temperature:   temperature,
	}, providerConfig.Keys)
	defer sessions.Close()

	transportConfig, err := transport.LoadConfig()
	if err != nil {
		return err
	}
	if !transportConfig.DiscordEnabled {
		log.Printf("no transports enabled; set DISCORD_BOT_TOKEN or add another transport adapter")
		return nil
	}

	discord, err := discordtransport.NewGateway(transportConfig.DiscordToken)
	if err != nil {
		return err
	}
	defer discord.Close(context.Background())
	if err := discord.Start(ctx); err != nil {
		return err
	}

	loop := &sdk.HarnessLoop{
		Client:         rt.Client,
		Source:         discord,
		ResolveSession: sessions.Resolve,
		BuildRequest: func(context.Context, sdk.Input, *sdk.Session) (sdk.Request, error) {
			return sdk.Request{
				SystemPrompt:    os.Getenv("AI_SYSTEM_PROMPT"),
				Temperature:     temperature,
				ThinkingLevel:   thinking,
				MaxOutputTokens: maxOutputTokens,
			}, nil
		},
		Displays:       []sdk.Display{discord},
		DisplayTimeout: 10 * time.Second,
		OnTurnError: func(input sdk.Input, err error) {
			log.Printf("turn failed source=%s session=%s: %v", input.Source, input.SessionID, err)
		},
	}
	return loop.Run(ctx)
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envFloat(name string) (*float64, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return &parsed, nil
}

func envInt(name string) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, nil
}
