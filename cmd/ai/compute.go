package main

import (
    "context"
    "errors"
    "fmt"
    "os"
    "os/signal"
    "path/filepath"
    "strings"
    "syscall"

    "github.com/Tulipskun/ai/runtime"
    "github.com/Tulipskun/ai/sdk"
)

func runCompute() error {
    state, err := stateRoot()
    if err != nil { return err }
    if err := os.MkdirAll(state, 0755); err != nil { return err }

    cloudflareConfig, err := runtime.LoadCloudflareConfig(filepath.Join(state, runtime.DefaultCloudflareConfigPath))
    if err != nil { return err }
    poll, lease, timeout, err := cloudflareConfig.Durations()
    if err != nil { return err }

    providerPath := filepath.Join(state, runtime.DefaultProviderConfigPath)
    providerFile, err := runtime.LoadProviderFile(providerPath)
    if err != nil { return err }
    rt, err := runtime.Load(providerPath)
    if err != nil { return err }
    if len(rt.ProviderConfigs) == 0 { return errors.New("compute: no providers configured") }

    providerID := strings.TrimSpace(os.Getenv("AI_PROVIDER"))
    if providerID == "" && len(rt.ProviderConfigs) == 1 {
        providerID = string(rt.ProviderConfigs[0].ID)
    }
    modelID := strings.TrimSpace(os.Getenv("AI_MODEL"))

    storeConfig := sdk.CloudflareStoreConfig{
        BaseURL: cloudflareConfig.Endpoint,
        Token: cloudflareConfig.ComputeToken,
        Timeout: timeout,
    }
    sessions := runtime.NewCloudflareSessionManager(storeConfig, sdk.SessionConfig{
        Provider: sdk.ProviderID(providerID),
        Model: modelID,
    }, rt.ProviderConfigs)
    defer sessions.Close()

    browserConfig, err := runtime.LoadBrowserConfig(filepath.Join(state, runtime.DefaultBrowserConfigPath))
    if err != nil { return err }
    if browserConfig.Enabled {
        rt.PrepareBrowser(browserConfig, state)
        defer rt.CloseBrowser()
    }

    workspace, err := resolveWorkspace()
    if err != nil { return err }
    agent, err := newAgentWithWorkspaces(
        rt.Client, workspace, rt.Browser, browserConfig.AllowPrivate,
        filepath.Join(state, "data", "jobs.json"), nil, sessions.WorkspaceFor,
    )
    if err != nil { return err }

    maxOutputTokens, err := envInt("AI_MAX_OUTPUT_TOKENS")
    if err != nil { return err }

    client := runtime.NewCloudflareComputeClient(storeConfig, poll, lease)
    worker := runtime.NewCloudflareComputeWorker(
        client,
        sessions,
        agent,
        func(ctx context.Context, input sdk.Input, session *sdk.Session) (sdk.Request, error) {
            cfg := session.Config()
            model := strings.TrimSpace(input.Metadata["model"])
            if model == "" { model = cfg.Model }
            return sdk.Request{
                Provider: cfg.Provider,
                Model: model,
                SystemPrompt: systemPrompt(agent),
                MaxOutputTokens: maxOutputTokens,
            }, nil
        },
        strings.TrimSpace(os.Getenv("AI_WORKER_ID")),
    )

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()
    return worker.Run(ctx)
}

func printComputeConfig() {
    state, err := stateRoot()
    if err != nil { fmt.Fprintln(os.Stderr, err); return }
    fmt.Println(filepath.Join(state, runtime.DefaultCloudflareConfigPath))
}
