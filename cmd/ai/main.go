package main

import (
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
	if errors.Is(err, errHelp) { printUsage(os.Stdout); return }
	if err != nil { printUsage(os.Stderr); log.Fatal(err) }
	switch command {
	case commandStart:
		if err := runBackground(); err != nil { log.Fatal(err) }
	case commandCLI:
		if err := runCLI(); err != nil && !errors.Is(err, context.Canceled) { log.Fatal(err) }
	case commandUpdate:
		if err := runUpdate(); err != nil { log.Fatal(err) }
	case commandDaemon:
		if err := runDaemon(); err != nil && !errors.Is(err, context.Canceled) { log.Fatal(err) }
	case commandUninstall:
		if err := runUninstall(); err != nil { log.Fatal(err) }
	}
}

func runBackground() error {
	app, err := installedBinary()
	if err != nil { return err }
	return startDaemon(app)
}

func startDaemon(app string) error {
	state, err := stateRoot()
	if err != nil { return err }
	stateDir := filepath.Join(state, ".ai")
	if err := os.MkdirAll(stateDir, 0o755); err != nil { return err }
	pidPath := filepath.Join(stateDir, "ai.pid")
	if data, err := os.ReadFile(pidPath); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && processAlive(pid) {
			return fmt.Errorf("ai is already running (pid %d)", pid)
		}
		_ = os.Remove(pidPath)
	}
	logFile, err := os.OpenFile(filepath.Join(stateDir, "ai.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil { return err }
	cmd := exec.Command(app, "daemon")
	cmd.Dir = state
	cmd.Env = os.Environ()
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil { _ = logFile.Close(); return err }
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
	if err != nil { return err }
	pidPath := filepath.Join(state, ".ai", "ai.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) { return nil }
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 { _ = os.Remove(pidPath); return nil }
	if !processAlive(pid) { _ = os.Remove(pidPath); return nil }
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) { return err }
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) { _ = os.Remove(pidPath); return nil }
		time.Sleep(100 * time.Millisecond)
	}
	if processAlive(pid) { _ = syscall.Kill(pid, syscall.SIGKILL) }
	_ = os.Remove(pidPath)
	return nil
}

func runDaemon() error {
	state, err := stateRoot()
	if err != nil { return err }
	if err := os.MkdirAll(state, 0o755); err != nil { return err }
	if err := os.Chdir(state); err != nil { return err }
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, false)
}

func runCLI() error {
	state, err := stateRoot()
	if err != nil { return err }
	if err := os.MkdirAll(state, 0o755); err != nil { return err }
	if err := os.Chdir(state); err != nil { return err }
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return run(ctx, true)
}

func run(ctx context.Context, cliOnly bool) error {
	state, err := stateRoot()
	if err != nil { return err }
	providerConfigPath := filepath.Join(state, runtime.DefaultProviderConfigPath)
	providerFile, err := runtime.LoadProviderFile(providerConfigPath); if err != nil { return err }
	rt, err := runtime.Load(providerConfigPath); if err != nil { return err }
	providerManager := runtime.NewProviderManager(providerConfigPath, rt, providerFile)
	maxOutputTokens, err := envInt("AI_MAX_OUTPUT_TOKENS"); if err != nil { return err }
	sessionDB := filepath.Join(state, ".data/sessions.db")
	providerID := strings.TrimSpace(os.Getenv("AI_PROVIDER")); if providerID == "" && len(rt.ProviderConfigs) == 1 { providerID = string(rt.ProviderConfigs[0].ID) }
	modelID := strings.TrimSpace(os.Getenv("AI_MODEL"))
	sessions := runtime.NewSessionManagerWithProviders(sessionDB, sdk.SessionConfig{Provider:sdk.ProviderID(providerID), Model:modelID}, rt.ProviderConfigs); defer sessions.Close()
	browserConfig, err := runtime.LoadBrowserConfig(); if err != nil { return err }
	if browserConfig.Enabled { if err := rt.StartBrowser(ctx, browserConfig); err != nil { return fmt.Errorf("start browser: %w", err) }; defer rt.CloseBrowser() }
	inputConfigPath := filepath.Join(state, transport.DefaultConfigPath)
	transportConfig, err := transport.LoadConfig(inputConfigPath); if err != nil { return err }
	if !cliOnly && !transportConfig.DiscordEnabled { log.Printf("no Discord transport enabled; configure .config/input.json") }
	workspace := envOr("AI_WORKSPACE", ".")
	agent, err := newAgent(rt.Client, workspace, rt.Browser, browserConfig.AllowPrivate); if err != nil { return err }
	providerKeys := make(map[sdk.ProviderID]*sdk.KeyPool, len(rt.ProviderConfigs)); for _, provider := range rt.ProviderConfigs { providerKeys[provider.ID] = provider.Keys }
	if cliOnly { return runInteractiveCLI(ctx, sessions, agent, providerManager, providerKeys, rt, maxOutputTokens) }
	var sources []sdk.InputSource; var displays []sdk.Display
	if transportConfig.DiscordEnabled {
		discord, err := discordtransport.NewGateway(transportConfig.DiscordToken); if err != nil { return err }; defer discord.Close(context.Background()); discord.ConfigureAuthorizedUser(transportConfig.DiscordOwnerID)
		modelSettings := &discordtransport.ModelSettingsHandler{ResolveSession:sessions.Resolve, Providers:rt.Providers, ProviderKeys:providerKeys, Models:func(ctx context.Context, provider sdk.ProviderID)([]sdk.Model,error){models:=rt.Router.Models(provider);if len(models)==0{if err:=rt.RefreshProvider(ctx,provider);err!=nil{return nil,err};models=rt.Router.Models(provider)};return models,nil}}
		discord.ConfigureModelSettings(modelSettings); discord.ConfigureProviderSettings(&discordtransport.ProviderSettingsHandler{Adapters:providerManager.Adapters(), Upsert:func(ctx context.Context,name,adapter,endpoint,apiKey string)error{if err:=providerManager.Upsert(ctx,name,adapter,endpoint,apiKey);err!=nil{return err};config,err:=rt.Router.Provider(sdk.ProviderID(name));if err!=nil{return err};sessions.RegisterProvider(config.ID,config.Keys);providerKeys[config.ID]=config.Keys;modelSettings.Providers=providerManager.Providers();modelSettings.ProviderKeys=providerKeys;return nil}}); discord.ConfigureStop(agent.Interrupt)
		if err:=discord.Start(ctx);err!=nil{return err}; sources=append(sources,discord); displays=append(displays,discord)
	}
	cli:=clitransport.New(os.Stdin,os.Stdout); cli.Command=func(ctx context.Context,args []string)(string,error){if len(args)==0{if len(providerManager.Providers())==0{return "No providers configured. Use: /provider add <name> <adapter> <url> <api-key>",nil};return "Providers: "+joinProviderIDs(providerManager.Providers()),nil};if args[0]!="add"||len(args)!=5{return "Usage: /provider add <name> <adapter> <url> <api-key>",nil};name:=args[1];if err:=providerManager.Upsert(ctx,name,args[2],args[3],args[4]);err!=nil{return "",err};config,err:=rt.Router.Provider(sdk.ProviderID(name));if err!=nil{return "",err};sessions.RegisterProvider(config.ID,config.Keys);providerKeys[config.ID]=config.Keys;return fmt.Sprintf("Provider %q saved and model catalogue refreshed. Configure AI_MODEL to use it.",name),nil};if transportConfig.CLIEnabled{sources=append(sources,cli);displays=append(displays,clitransport.NewDisplay(os.Stdout))};if len(sources)==0{return fmt.Errorf("no transports enabled; configure .config/input.json")};inputs,err:=sdk.MergeInputSources(ctx,sources...);if err!=nil{return err};loop:=&sdk.HarnessLoop{Agent:agent,Source:sdk.ChannelInputSource{Inputs:inputs},ResolveSession:sessions.Resolve,BuildRequest:func(context.Context,sdk.Input,*sdk.Session)(sdk.Request,error){return sdk.Request{SystemPrompt:os.Getenv("AI_SYSTEM_PROMPT"),MaxOutputTokens:maxOutputTokens},nil},Displays:displays,DisplayTimeout:10*time.Second,OnTurnError:func(input sdk.Input,err error){log.Printf("turn failed source=%s session=%s: %v",input.Source,input.SessionID,err)}};err=loop.Run(ctx);if app,e:=installedBinary();e==nil{if state,e:=stateRoot();e==nil{_=os.Remove(filepath.Join(state,".ai","ai.pid"))}; _ = app};return err}

func processAlive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }
func newAgent(client *sdk.RouterClient,workspace string,browser *tools.BrowserClient,allowPrivate bool)(*sdk.Agent,error){registry,err:=tools.NewRegistryWithBrowser(workspace,browser,allowPrivate);if err!=nil{return nil,err};return &sdk.Agent{Client:client,Tools:registry},nil}
func envOr(name,fallback string)string{if value:=strings.TrimSpace(os.Getenv(name));value!=""{return value};return fallback}
func envInt(name string)(int,error){value:=strings.TrimSpace(os.Getenv(name));if value==""{return 0,nil};parsed,err:=strconv.Atoi(value);if err!=nil{return 0,fmt.Errorf("%s: %w",name,err)};return parsed,nil}
