package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Tulipskun/ai/runtime"
	"github.com/Tulipskun/ai/sdk"
	"github.com/Tulipskun/ai/tools"
	"github.com/Tulipskun/ai/transport"
	clitransport "github.com/Tulipskun/ai/transport/cli"
	discordtransport "github.com/Tulipskun/ai/transport/discord"
)

func main() {
	command, err := parseCommand(os.Args[1:])
	if errors.Is(err, errHelp) {
		printUsage(os.Stdout)
		return
	}
	if err != nil {
		printUsage(os.Stderr)
		log.Fatal(err)
	}
	switch command {
	case commandStart:
		if err := runBackground(); err != nil {
			log.Fatal(err)
		}
	case commandCLI:
		if err := runCLI(); err != nil && !errors.Is(err, context.Canceled) {
			log.Fatal(err)
		}
	case commandDiscord:
		if err := runDiscordConfig(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case commandBrowser:
		if err := runBrowserConfig(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case commandSystem:
		if err := runSystemConfig(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case commandUpdate:
		if err := runUpdate(); err != nil {
			log.Fatal(err)
		}
	case commandDaemon:
		if err := runDaemon(); err != nil && !errors.Is(err, context.Canceled) {
			log.Fatal(err)
		}
	case commandStop:
		if err := runStop(); err != nil {
			log.Fatal(err)
		}
	case commandUninstall:
		if err := runUninstall(); err != nil {
			log.Fatal(err)
		}
	}
}

func runDiscordConfig(args []string) error {
	state, err := stateRoot()
	if err != nil {
		return err
	}
	path := filepath.Join(state, transport.DefaultConfigPath)
	current, err := transport.LoadConfig(path)
	if err != nil {
		return err
	}
	if len(args) == 1 && args[0] == "disable" {
		current.Discord.Enabled = false
		if err := transport.SaveConfig(path, current); err != nil {
			return err
		}
		fmt.Printf("Discord disabled. Config: %s\n", path)
		return nil
	}
	if len(args) != 0 {
		return errors.New("discord: usage is 'ai discord' or 'ai discord disable'")
	}
	reader := bufio.NewReader(os.Stdin)
	token, err := prompt(reader, "Discord token: ")
	if err != nil {
		return err
	}
	ownerID, err := prompt(reader, "Discord owner ID: ")
	if err != nil {
		return err
	}
	if token == "" {
		return errors.New("discord: token cannot be empty")
	}
	if ownerID == "" {
		return errors.New("discord: owner ID cannot be empty")
	}
	current.Discord.Token = token
	current.Discord.OwnerID = ownerID
	current.Discord.Enabled = true
	if err := transport.SaveConfig(path, current); err != nil {
		return err
	}
	fmt.Printf("Discord config saved to %s\n", path)
	return nil
}

func prompt(reader *bufio.Reader, label string) (string, error) {
	fmt.Print(label)
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, os.ErrClosed) && len(value) == 0 {
		return "", err
	}
	return strings.TrimSpace(value), nil
}
func promptDefault(reader *bufio.Reader, label, def string) (string, error) {
	value, err := prompt(reader, fmt.Sprintf("%s [%s]: ", label, def))
	if err != nil {
		return "", err
	}
	if value == "" {
		return def, nil
	}
	return value, nil
}
func promptBool(reader *bufio.Reader, label string, def bool) (bool, error) {
	hint := "y/n"
	if def {
		hint = "Y/n"
	} else {
		hint = "y/N"
	}
	value, err := prompt(reader, fmt.Sprintf("%s (%s): ", label, hint))
	if err != nil {
		return false, err
	}
	if value == "" {
		return def, nil
	}
	switch strings.ToLower(value) {
	case "y", "yes", "true", "1":
		return true, nil
	case "n", "no", "false", "0":
		return false, nil
	}
	return false, fmt.Errorf("browser: answer y or n for %q", label)
}
func runSystemConfig(args []string) error {
	state, err := stateRoot()
	if err != nil {
		return err
	}
	path := filepath.Join(state, runtime.DefaultSystemConfigPath)
	if len(args) >= 1 && args[0] == "clear" {
		if err := runtime.SaveSystemConfig(path, runtime.SystemConfig{}); err != nil {
			return err
		}
		fmt.Printf("System prompt cleared, using built-in default. Config: %s\n", path)
		return nil
	}
	if len(args) >= 2 && args[0] == "set" {
		text := strings.TrimSpace(strings.Join(args[1:], " "))
		if text == "" {
			return errors.New("system: usage is 'ai system set <prompt>'")
		}
		if err := runtime.SaveSystemConfig(path, runtime.SystemConfig{SystemPrompt: text}); err != nil {
			return err
		}
		fmt.Printf("System prompt saved to %s\n", path)
		return nil
	}
	if len(args) != 0 {
		return errors.New("system: usage is 'ai system', 'ai system set <prompt>' or 'ai system clear'")
	}
	fmt.Printf("Source: %s\n\n%s\n", systemPromptSource(), systemPrompt(nil))
	return nil
}

func runBrowserConfig(args []string) error {
	state, err := stateRoot()
	if err != nil {
		return err
	}
	path := filepath.Join(state, runtime.DefaultBrowserConfigPath)
	current, err := runtime.LoadBrowserConfig(path)
	if err != nil {
		return err
	}
	if len(args) == 1 && args[0] == "disable" {
		current.Enabled = false
		if err := runtime.SaveBrowserConfig(path, current); err != nil {
			return err
		}
		fmt.Printf("Browser automation disabled. Config: %s\n", path)
		return nil
	}
	if len(args) != 0 {
		return errors.New("browser: usage is 'ai browser' or 'ai browser disable'")
	}
	reader := bufio.NewReader(os.Stdin)
	enabled, err := promptBool(reader, "Enable browser automation", current.Enabled)
	if err != nil {
		return err
	}
	current.Enabled = enabled
	mode, err := promptDefault(reader, "Mode (managed/attach)", current.Mode)
	if err != nil {
		return err
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "managed" && mode != "attach" {
		return fmt.Errorf("browser: mode must be managed or attach")
	}
	current.Mode = mode
	if mode == "attach" {
		endpoint, err := promptDefault(reader, "CDP endpoint (http://127.0.0.1:9222)", current.CDPEndpoint)
		if err != nil {
			return err
		}
		if strings.TrimSpace(endpoint) == "" {
			return errors.New("browser: cdp_endpoint is required in attach mode")
		}
		current.CDPEndpoint = strings.TrimSpace(endpoint)
	} else {
		browser, err := promptDefault(reader, "Browser (auto/chrome/chromium/edge)", current.Browser)
		if err != nil {
			return err
		}
		browser = strings.ToLower(strings.TrimSpace(browser))
		switch browser {
		case "auto", "chrome", "chromium", "edge":
		default:
			return fmt.Errorf("browser must be one of auto, chrome, chromium, edge")
		}
		current.Browser = browser
		profile, err := promptDefault(reader, "Profile directory", current.Profile)
		if err != nil {
			return err
		}
		if strings.TrimSpace(profile) == "" {
			return errors.New("browser: profile is required in managed mode")
		}
		current.Profile = strings.TrimSpace(profile)
		headless, err := promptBool(reader, "Headless", current.Headless)
		if err != nil {
			return err
		}
		current.Headless = headless
		if !headless {
			dispDef := current.Display
			if strings.TrimSpace(dispDef) == "" {
				dispDef = ":1"
			}
			disp, err := promptDefault(reader, "Display", dispDef)
			if err != nil {
				return err
			}
			current.Display = strings.TrimSpace(disp)
		} else {
			current.Display = ""
		}
	}
	allowPrivate, err := promptBool(reader, "Allow private pages", current.AllowPrivate)
	if err != nil {
		return err
	}
	current.AllowPrivate = allowPrivate
	if err := runtime.SaveBrowserConfig(path, current); err != nil {
		return err
	}
	fmt.Printf("Browser config saved to %s\n", path)
	return nil
}
func runBackground() error {
	app, err := installedBinary()
	if err != nil {
		return err
	}
	return startDaemon(app)
}
func startDaemon(app string) error {
	state, err := stateRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(state, 0o755); err != nil {
		return err
	}
	lockPath := filepath.Join(state, "ai.start.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("ai start is already in progress")
		}
		return err
	}
	defer func() { _ = lock.Close(); _ = os.Remove(lockPath) }()
	pidPath := filepath.Join(state, "ai.pid")
	if data, err := os.ReadFile(pidPath); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && processAlive(pid) {
			return fmt.Errorf("ai is already running (pid %d)", pid)
		}
		_ = os.Remove(pidPath)
	}
	logFile, err := os.OpenFile(filepath.Join(state, "ai.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(app, "daemon")
	cmd.Dir = state
	cmd.Env = os.Environ()
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		_ = logFile.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Process.Release()
		return fmt.Errorf("failed to start ai daemon: invalid pid %d", pid)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		_ = cmd.Process.Kill()
		_ = logFile.Close()
		_ = cmd.Process.Release()
		return err
	}
	_ = logFile.Close()
	_ = cmd.Process.Release()
	fmt.Printf("[ai] started (pid %d)\n", pid)
	return nil
}
func stopDaemon(_ string) error {
	state, err := stateRoot()
	if err != nil {
		return err
	}
	pidPath := filepath.Join(state, "ai.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		_ = os.Remove(pidPath)
		return nil
	}
	if !processAlive(pid) {
		_ = os.Remove(pidPath)
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			_ = os.Remove(pidPath)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if processAlive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	_ = os.Remove(pidPath)
	return nil
}
func runStop() error {
	state, err := stateRoot()
	if err != nil {
		return err
	}
	wasRunning := daemonRunning()
	if err := stopDaemon(""); err != nil {
		return err
	}
	stoppedKeepalive := stopKeepaliveWatchers()
	if daemonRunning() {
		return fmt.Errorf("ai daemon is still running")
	}
	if !wasRunning {
		fmt.Printf("[ai] not running (state: %s)\n", state)
		return nil
	}
	if stoppedKeepalive > 0 {
		fmt.Printf("[ai] stopped (pid file removed, %d keepalive watcher(s) stopped)\n", stoppedKeepalive)
	} else {
		fmt.Printf("[ai] stopped\n")
	}
	return nil
}

// stopKeepaliveWatchers SIGTERMs detached keepalive.sh loops that would
// otherwise restart the daemon right after 'ai stop'. Returns how many
// watchers were signaled. A SIGKILLed keeper cannot run its EXIT trap, so a
// stale keepalive.lock directory is removed when no watcher remains.
func stopKeepaliveWatchers() int {
	self := os.Getpid()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	var targets []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || pid == self {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil || len(cmdline) == 0 {
			continue
		}
		cmd := strings.ReplaceAll(string(cmdline), "\x00", " ")
		if !strings.Contains(cmd, "keepalive.sh") {
			continue
		}
		targets = append(targets, pid)
	}
	for _, pid := range targets {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		alive := false
		for _, pid := range targets {
			if processAlive(pid) {
				alive = true
				break
			}
		}
		if !alive {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, pid := range targets {
		if processAlive(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	if state, err := stateRoot(); err == nil {
		keeperAlive := false
		for _, pid := range targets {
			if processAlive(pid) {
				keeperAlive = true
				break
			}
		}
		if !keeperAlive {
			_ = os.Remove(filepath.Join(state, "keepalive.lock"))
		}
	}
	stopped := 0
	for _, pid := range targets {
		if !processAlive(pid) {
			stopped++
		}
	}
	return stopped
}
func runDaemon() error {
	state, err := stateRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(state, 0o755); err != nil {
		return err
	}
	if err := os.Chdir(state); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, false)
}
func runCLI() error {
	state, err := stateRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(state, 0o755); err != nil {
		return err
	}
	if err := os.Chdir(state); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, true)
}
func run(ctx context.Context, cliOnly bool) error {
	state, err := stateRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(state, "data"), 0o755); err != nil {
		return err
	}
	providerConfigPath := filepath.Join(state, runtime.DefaultProviderConfigPath)
	providerFile, err := runtime.LoadProviderFile(providerConfigPath)
	if err != nil {
		return err
	}
	rt, err := runtime.Load(providerConfigPath)
	if err != nil {
		return err
	}
	providerManager := runtime.NewProviderManager(providerConfigPath, rt, providerFile)
	maxOutputTokens, err := envInt("AI_MAX_OUTPUT_TOKENS")
	if err != nil {
		return err
	}
	sessionDB := filepath.Join(state, "data", "sessions.db")
	providerID := strings.TrimSpace(os.Getenv("AI_PROVIDER"))
	if providerID == "" && len(rt.ProviderConfigs) == 1 {
		providerID = string(rt.ProviderConfigs[0].ID)
	}
	modelID := strings.TrimSpace(os.Getenv("AI_MODEL"))
	sessions := runtime.NewSessionManagerWithProviders(sessionDB, sdk.SessionConfig{Provider: sdk.ProviderID(providerID), Model: modelID}, rt.ProviderConfigs)
	defer sessions.Close()
	browserConfig, err := runtime.LoadBrowserConfig(filepath.Join(state, runtime.DefaultBrowserConfigPath))
	if err != nil {
		return err
	}
	if browserConfig.Enabled {
		if err := rt.StartBrowser(ctx, browserConfig, state); err != nil {
			log.Printf("browser unavailable, continuing without browser automation: %v", err)
		}
		defer rt.CloseBrowser()
	}
	inputConfigPath := filepath.Join(state, transport.DefaultConfigPath)
	transportConfig, err := transport.LoadConfig(inputConfigPath)
	if err != nil {
		return err
	}
	if !cliOnly && !transportConfig.Discord.Enabled {
		log.Printf("no Discord transport enabled; configure config/entry.json")
	}
	workspace, err := resolveWorkspace()
	if err != nil {
		return err
	}
	jobsPath := filepath.Join(state, "data", "jobs.json")
	agent, err := newAgent(rt.Client, workspace, rt.Browser, browserConfig.AllowPrivate, jobsPath)
	if err != nil {
		return err
	}
	providerKeys := make(map[sdk.ProviderID]*sdk.KeyPool, len(rt.ProviderConfigs))
	for _, provider := range rt.ProviderConfigs {
		providerKeys[provider.ID] = provider.Keys
	}
	if cliOnly {
		return runInteractiveCLI(ctx, sessions, agent, providerManager, providerKeys, rt, maxOutputTokens)
	}
	var sources []sdk.InputSource
	var displays []sdk.Display
	if transportConfig.Discord.Enabled {
		discord, err := discordtransport.NewGateway(transportConfig.Discord.Token)
		if err != nil {
			return err
		}
		defer discord.Close(context.Background())
		discord.ConfigureAuthorizedUser(transportConfig.Discord.OwnerID)
		discord.ConfigureSessionList(sessions.ListSessions)
		modelSettings := &discordtransport.ModelSettingsHandler{ResolveSession: sessions.Resolve, SessionForChannel: discord.SessionIDForChannel, Providers: rt.Providers, ProviderKeys: providerKeys, Models: func(ctx context.Context, provider sdk.ProviderID) ([]sdk.Model, error) {
			models := rt.Router.Models(provider)
			if len(models) == 0 {
				if err := rt.RefreshProvider(ctx, provider); err != nil {
					return nil, err
				}
				models = rt.Router.Models(provider)
			}
			return models, nil
		}}
		discord.ConfigureModelSettings(modelSettings)
		discord.ConfigureProviderSettings(&discordtransport.ProviderSettingsHandler{Adapters: providerManager.Adapters(), Upsert: func(ctx context.Context, name, adapter, endpoint, apiKey string, freeOnly bool) error {
			if err := providerManager.Upsert(ctx, name, adapter, endpoint, apiKey, freeOnly); err != nil {
				return err
			}
			config, err := rt.Router.Provider(sdk.ProviderID(name))
			if err != nil {
				return err
			}
			sessions.RegisterProvider(config.ID, config.Keys)
			providerKeys[config.ID] = config.Keys
			modelSettings.Providers = providerManager.Providers()
			modelSettings.ProviderKeys = providerKeys
			return nil
		}})
		discord.ConfigureStop(agent.Interrupt)
		if err := discord.Start(ctx); err != nil {
			return err
		}
		sources = append(sources, discord)
		displays = append(displays, discord)
	}
	cli := clitransport.New(os.Stdin, os.Stdout)
	cli.Command = func(ctx context.Context, args []string) (string, error) {
		if len(args) == 0 {
			if len(providerManager.Providers()) == 0 {
				return "No providers configured. Use: /provider add <name> <adapter> <url> <api-key>", nil
			}
			return "Providers: " + joinProviderIDs(providerManager.Providers()), nil
		}
		if args[0] != "add" || (len(args) != 5 && len(args) != 6) {
			return "Usage: /provider add <name> <adapter> <url> <api-key> [free]", nil
		}
		name := args[1]
		freeOnly := len(args) == 6 && strings.EqualFold(args[5], "free")
		if len(args) == 6 && !freeOnly {
			return "Usage: /provider add <name> <adapter> <url> <api-key> [free]", nil
		}
		if err := providerManager.Upsert(ctx, name, args[2], args[3], args[4], freeOnly); err != nil {
			return "", err
		}
		config, err := rt.Router.Provider(sdk.ProviderID(name))
		if err != nil {
			return "", err
		}
		sessions.RegisterProvider(config.ID, config.Keys)
		providerKeys[config.ID] = config.Keys
		return fmt.Sprintf("Provider %q saved and model catalogue refreshed. Configure AI_MODEL to use it.", name), nil
	}
	if transportConfig.CLI.Enabled {
		sources = append(sources, cli)
		displays = append(displays, clitransport.NewDisplay(os.Stdout))
	}
	if len(sources) == 0 {
		return fmt.Errorf("no transports enabled; configure config/entry.json")
	}
	inputs, err := sdk.MergeInputSources(ctx, sources...)
	if err != nil {
		return err
	}
	loop := &sdk.HarnessLoop{Agent: agent, Source: sdk.ChannelInputSource{Inputs: inputs}, ResolveSession: sessions.Resolve, BuildRequest: func(context.Context, sdk.Input, *sdk.Session) (sdk.Request, error) {
		return sdk.Request{SystemPrompt: systemPrompt(agent), MaxOutputTokens: maxOutputTokens}, nil
	}, Displays: displays, DisplayTimeout: 10 * time.Second, OnTurnError: func(input sdk.Input, err error) {
		log.Printf("turn failed source=%s session=%s: %v", input.Source, input.SessionID, err)
	}}
	err = loop.Run(ctx)
	if app, e := installedBinary(); e == nil {
		if state, e := stateRoot(); e == nil {
			_ = os.Remove(filepath.Join(state, "ai.pid"))
		}
		_ = app
	}
	return err
}
func processAlive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }
func newAgent(client *sdk.RouterClient, workspace string, browser *tools.BrowserClient, allowPrivate bool, jobsPath string) (*sdk.Agent, error) {
	registry, err := tools.NewRegistryWithBrowser(workspace, browser, allowPrivate, jobsPath)
	if err != nil {
		return nil, err
	}
	return &sdk.Agent{Client: client, Tools: registry}, nil
}
func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
func systemPrompt(agent *sdk.Agent) string {
	if value := strings.TrimSpace(os.Getenv("AI_SYSTEM_PROMPT")); value != "" {
		return value
	}
	if state, err := stateRoot(); err == nil {
		if cfg, err := runtime.LoadSystemConfig(filepath.Join(state, runtime.DefaultSystemConfigPath)); err == nil && cfg.SystemPrompt != "" {
			return cfg.SystemPrompt
		}
	}
	return defaultSystemPrompt(agent)
}

func systemPromptSource() string {
	if state, err := stateRoot(); err == nil {
		path := filepath.Join(state, runtime.DefaultSystemConfigPath)
		if cfg, err := runtime.LoadSystemConfig(path); err == nil && cfg.SystemPrompt != "" {
			return path
		}
	}
	return "built-in default"
}

func defaultSystemPrompt(agent *sdk.Agent) string {
	var b strings.Builder
	b.WriteString("You are an AI assistant that gets things done with tools.\n")
	b.WriteString("Stay strictly within the user's requested goal and scope. Do not start unrelated improvements, features, cleanup, or investigations.\n")
	b.WriteString("Before using any tool, create a concise plan with ordered steps for the user's goal. The plan describes goals and success conditions, not a fixed list of tools.\n")
	b.WriteString("Follow the current plan step until its success condition is satisfied. You may use any available tool needed to complete that step; do not restrict yourself to one exact tool or target.\n")
	b.WriteString("When a tool, command, build, test, or edit fails, diagnose the failure and fix it within the current step. A failure is not a reason to abandon the task or move to an unrelated step.\n")
	b.WriteString("Only mark a step complete after verifying that its intended result is actually achieved. After the final goal is complete, stop and send the final result.\n")
	b.WriteString("When the user asks to do, check, change, create, or fetch anything, CALL the matching tool instead of only describing what to do.\n")
	b.WriteString("Prefer acting first: inspect with read_file, list_directory, or search_files, then act. Batch independent tool calls together.\n")
	b.WriteString("Use run_command for shell work (it supports chains, pipes, and redirects). Use web_fetch for URLs. Use browser_* tools to operate web pages.\n")
	b.WriteString("After tool results, summarize briefly what you did. Match the user's language.\n")
	b.WriteString("Only when the task is complete and you are sending the final message to the user, start that final message with \u2728\u2728\u2728. Do not use \u2728\u2728\u2728 in intermediate progress, tool-related, or continuation messages.\n")
	b.WriteString("Never stop at a promise: if you say you will fetch, check, or run something, call the tool in the SAME response instead of ending your turn.\n")
	if agent != nil && agent.Tools != nil {
		if defs := agent.Tools.Definitions(); len(defs) > 0 {
			b.WriteString("Available tools:\n")
			for _, d := range defs {
				b.WriteString("- " + d.Name + ": " + strings.TrimSpace(d.Description) + "\n")
			}
		}
	}
	return b.String()
}
func resolveWorkspace() (string, error) {
	raw := strings.TrimSpace(os.Getenv("AI_WORKSPACE"))
	if raw == "" {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return ".", nil
		}
		return home, nil
	}
	path := expandHome(raw)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", fmt.Errorf("AI_WORKSPACE: %w", err)
	}
	return path, nil
}
func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			return filepath.Join(home, path[2:])
		}
	}
	return path
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
