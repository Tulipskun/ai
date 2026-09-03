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
	"github.com/Tulipskun/ai/tools"
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

	providerConfig, err := rt.Router.Provider(provider)
	if err != nil {
		return err
	}
	if providerConfig.Keys == nil {
		return errors.New("selected provider has no API keys")
	}

	maxOutputTokens, err := envInt("AI_MAX_OUTPUT_TOKENS")
	if err != nil {
		return err
	}

	sessionDB := envOr("AI_SESSION_DB", ".data/sessions.db")
	sessions := runtime.NewSessionManager(sessionDB, sdk.SessionConfig{
		Provider: provider,
		KeyIndex: 0,
	}, providerConfig.Keys)
	defer sessions.Close()

	browserConfig, err := runtime.LoadBrowserConfig()
	if err != nil {
		return err
	}
	if browserConfig.Enabled {
		if err := rt.StartBrowser(ctx, browserConfig); err != nil {
			return fmt.Errorf("start browser: %w", err)
		}
		defer rt.CloseBrowser()
	}

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

	workspace := envOr("AI_WORKSPACE", ".")
	agent, err := newAgent(rt.Client, workspace, rt.Browser, browserConfig.AllowPrivate)
	if err != nil {
		return err
	}

	loop := &sdk.HarnessLoop{
		Agent:          agent,
		Source:         discord,
		ResolveSession: sessions.Resolve,
		BuildRequest: func(context.Context, sdk.Input, *sdk.Session) (sdk.Request, error) {
			return sdk.Request{
				SystemPrompt:    os.Getenv("AI_SYSTEM_PROMPT"),
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

func newAgent(client *sdk.RouterClient, workspace string, browser *tools.BrowserClient, allowPrivate bool) (*sdk.Agent, error) {
	registry, err := tools.NewRegistryWithBrowser(workspace, browser, allowPrivate)
	if err != nil {
		return nil, err
	}
	return &sdk.Agent{
		Client:        client,
		Tools:         registry,
		MaxIterations: 20,
	}, nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
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
