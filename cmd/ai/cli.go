package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tulipskun/ai/runtime"
	"github.com/Tulipskun/ai/sdk"
	clitransport "github.com/Tulipskun/ai/transport/cli"
)

func runInteractiveCLI(ctx context.Context, sessions *runtime.SessionManager, agent *sdk.Agent, providerManager *runtime.ProviderManager, providerKeys map[sdk.ProviderID]*sdk.KeyPool, rt *runtime.Runtime, maxOutputTokens int) error {
	currentSession := "cli:default"
	var activeMu sync.Mutex
	var turnMu sync.Mutex
	activeTurn := ""
	ui := clitransport.NewUI(os.Stdout)
	editor := clitransport.NewLineEditor(os.Stdin, os.Stdout)
	editor.HistoryPath = filepath.Join(".data", "cli.history")
	if err := editor.LoadHistory(); err != nil {
		return err
	}
	editor.Prompt = func() string {
		provider, model := currentModelState(ctx, sessions, currentSession)
		return fmt.Sprintf("ai %s › ", clitransport.ModelLabel(provider, model))
	}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-sigCh:
				activeMu.Lock()
				id := activeTurn
				activeMu.Unlock()
				if id != "" && agent.Interrupt(id) {
					ui.Print("interrupt › cancelling current turn")
				}
			}
		}
	}()
	agent.SetSubAgentEventSink(func(event sdk.SubAgentEvent) {
		step := "investigation"
		if event.PlanStep.Index > 0 {
			step = fmt.Sprintf("plan step %d", event.PlanStep.Index)
		}
		text := fmt.Sprintf("Sub-agent job %s %s for %s: %s", event.JobID, event.Status, step, event.Result)
		go func() {
			turnMu.Lock()
			defer turnMu.Unlock()
			_, err := agent.RunTurnWithTrace(context.WithoutCancel(ctx), event.Parent, sdk.Turn{Role: sdk.RoleUser, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: text}}}, sdk.Request{SystemPrompt: systemPrompt(agent), MaxOutputTokens: maxOutputTokens, Stream: true}, func(_ context.Context, trace sdk.TraceEvent) { ui.Trace(trace) })
			ui.EndTurn()
			if err != nil && !errors.Is(err, context.Canceled) {
				ui.Print("sub-agent event turn failed: " + err.Error())
			}
		}()
	})
	shell := &clitransport.Shell{Editor: editor, Out: os.Stdout, Header: func() {
		provider, model := currentModelState(ctx, sessions, currentSession)
		ui.Header(currentSession, provider, model)
	}, Handle: func(ctx context.Context, line string) (string, bool, error) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "/") {
			return handleCLICommand(ctx, line, &currentSession, sessions, providerManager, providerKeys, rt)
		}
		input := sdk.Input{Source: clitransport.Source, SessionID: currentSession, Turn: sdk.Turn{Role: sdk.RoleUser, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: line}}}}
		session, err := sessions.Resolve(ctx, input)
		if err != nil {
			return "", false, err
		}
		turnMu.Lock()
		defer turnMu.Unlock()
		activeMu.Lock()
		activeTurn = session.ID()
		activeMu.Unlock()
		_, err = agent.RunTurnWithTrace(ctx, session, input.Turn, sdk.Request{SystemPrompt: systemPrompt(agent), MaxOutputTokens: maxOutputTokens, Stream: true}, func(_ context.Context, event sdk.TraceEvent) { ui.Trace(event) })
		activeMu.Lock()
		activeTurn = ""
		activeMu.Unlock()
		ui.EndTurn()
		if errors.Is(err, context.Canceled) {
			return "", false, nil
		}
		return "", false, err
	}}
	return shell.Run(ctx)
}

func handleCLICommand(ctx context.Context, line string, currentSession *string, sessions *runtime.SessionManager, providerManager *runtime.ProviderManager, providerKeys map[sdk.ProviderID]*sdk.KeyPool, rt *runtime.Runtime) (string, bool, error) {
	parts := strings.Fields(strings.TrimPrefix(line, "/"))
	if len(parts) == 0 {
		return "", false, nil
	}
	name, args := strings.ToLower(parts[0]), parts[1:]
	switch name {
	case "q", "quit", "exit":
		return "", true, nil
	case "help", "?":
		return cliHelp(), false, nil
	case "new", "clear":
		*currentSession = newCLISessionID()
		if name == "clear" {
			fmt.Fprint(os.Stdout, "\x1b[2J\x1b[H")
		}
		return "session › " + *currentSession, false, nil
	case "sessions":
		items, err := sessions.ListSessions(30)
		if err != nil {
			return "", false, err
		}
		if len(items) == 0 {
			return "No sessions.", false, nil
		}
		var b strings.Builder
		b.WriteString("Sessions:\n")
		for _, item := range items {
			marker := "  "
			if item.ID == *currentSession {
				marker = "* "
			}
			b.WriteString(fmt.Sprintf("%s%-18s %-30s %d turns\n", marker, clitransport.ShortID(item.ID), clitransport.ModelLabel(string(item.Provider), item.Model), item.TurnCount))
		}
		return strings.TrimRight(b.String(), "\n"), false, nil
	case "resume", "continue":
		id := ""
		if len(args) > 0 {
			id = args[0]
		}
		if id == "" {
			items, err := sessions.ListSessions(1)
			if err != nil {
				return "", false, err
			}
			if len(items) == 0 {
				return "No saved sessions.", false, nil
			}
			id = items[0].ID
		}
		items, err := sessions.ListSessions(100)
		if err != nil {
			return "", false, err
		}
		for _, item := range items {
			if item.ID == id || strings.HasSuffix(item.ID, id) {
				*currentSession = item.ID
				return "session › resumed " + item.ID, false, nil
			}
		}
		return "", false, fmt.Errorf("session not found: %s", id)
	case "provider":
		if len(args) == 0 {
			ids := providerManager.Providers()
			if len(ids) == 0 {
				return "No providers configured. Use: /provider add <name> <adapter> <url> <api-key> [free] [free]", false, nil
			}
			return "Providers: " + joinProviderIDs(ids), false, nil
		}
		if args[0] != "add" || (len(args) != 5 && len(args) != 6) {
			return "Usage: /provider add <name> <adapter> <url> <api-key> [free] [free]", false, nil
		}
		name, adapter, url, key := args[1], args[2], args[3], args[4]
		freeOnly := len(args) == 6 && strings.EqualFold(args[5], "free")
		if len(args) == 6 && !freeOnly {
			return "Usage: /provider add <name> <adapter> <url> <api-key> [free] [free]", false, nil
		}
		if err := providerManager.Upsert(ctx, name, adapter, url, key, freeOnly); err != nil {
			return "", false, err
		}
		config, err := rt.Router.Provider(sdk.ProviderID(name))
		if err != nil {
			return "", false, err
		}
		sessions.RegisterProvider(config.ID, config.Keys)
		providerKeys[config.ID] = config.Keys
		return fmt.Sprintf("Provider %q saved and models refreshed.", name), false, nil
	case "models":
		provider := ""
		if len(args) > 0 {
			provider = args[0]
		} else {
			provider, _ = currentModelState(ctx, sessions, *currentSession)
		}
		if provider == "" {
			return "No provider selected.", false, nil
		}
		models := rt.Router.Models(sdk.ProviderID(provider))
		if len(models) == 0 {
			if err := rt.RefreshProvider(ctx, sdk.ProviderID(provider)); err != nil {
				return "", false, err
			}
			models = rt.Router.Models(sdk.ProviderID(provider))
		}
		if len(models) == 0 {
			return "No models discovered for " + provider, false, nil
		}
		var b strings.Builder
		b.WriteString("Models (" + provider + "):\n")
		for _, model := range models {
			flags := ""
			if model.SupportsTools {
				flags += " tools"
			}
			if model.SupportsThinking {
				flags += " thinking"
			}
			if model.SupportsStreaming {
				flags += " stream"
			}
			b.WriteString("  " + model.ID + flags + "\n")
		}
		return strings.TrimRight(b.String(), "\n"), false, nil
	case "model":
		if len(args) != 1 {
			return "Usage: /model <model> or /model <provider>/<model>", false, nil
		}
		provider, model := splitModel(args[0])
		session, err := sessions.Resolve(ctx, sdk.Input{Source: clitransport.Source, SessionID: *currentSession})
		if err != nil {
			return "", false, err
		}
		selectedProvider := session.Config().Provider
		if provider != "" {
			selectedProvider = sdk.ProviderID(provider)
		}
		if err := validateCLIModel(ctx, rt, selectedProvider, model); err != nil {
			return "", false, err
		}
		if provider != "" && provider != string(session.Config().Provider) {
			config, err := rt.Router.Provider(sdk.ProviderID(provider))
			if err != nil {
				return "", false, err
			}
			if err := session.SetProvider(config.ID, config.Keys); err != nil {
				return "", false, err
			}
		}
		if err := session.SetModel(model); err != nil {
			return "", false, err
		}
		return "model › " + clitransport.ModelLabel(string(selectedProvider), model), false, nil
	case "thinking":
		if len(args) != 1 {
			return "Usage: /thinking <none|low|medium|high|off>", false, nil
		}
		level := sdk.ThinkingLevel(strings.ToLower(args[0]))
		if level == "off" {
			level = sdk.ThinkingNone
		}
		session, err := sessions.Resolve(ctx, sdk.Input{Source: clitransport.Source, SessionID: *currentSession})
		if err != nil {
			return "", false, err
		}
		if err := session.SetThinkingLevel(level); err != nil {
			return "", false, err
		}
		return "thinking › " + string(level), false, nil
	case "temperature":
		if len(args) != 1 {
			return "Usage: /temperature <0..2> or /temperature off", false, nil
		}
		session, err := sessions.Resolve(ctx, sdk.Input{Source: clitransport.Source, SessionID: *currentSession})
		if err != nil {
			return "", false, err
		}
		if strings.EqualFold(args[0], "off") {
			if err := session.ClearTemperature(); err != nil {
				return "", false, err
			}
			return "temperature › auto", false, nil
		}
		value, err := strconv.ParseFloat(args[0], 64)
		if err != nil || value < 0 || value > 2 {
			return "temperature must be between 0 and 2", false, nil
		}
		if err := session.SetTemperature(value); err != nil {
			return "", false, err
		}
		return "temperature › " + strconv.FormatFloat(value, 'g', -1, 64), false, nil
	case "status", "details":
		session, err := sessions.Resolve(ctx, sdk.Input{Source: clitransport.Source, SessionID: *currentSession})
		if err != nil {
			return "", false, err
		}
		config := session.Config()
		return fmt.Sprintf("session  %s\nprovider %s\nmodel    %s\nthinking %s\ntemp     %s\ncwd      %s", *currentSession, config.Provider, config.Model, config.ThinkingLevel, temperatureLabel(config.Temperature), mustGetwd()), false, nil
	case "export":
		return exportCLISession(ctx, sessions, *currentSession)
	case "compact":
		return "context › automatic context window is enabled; explicit compaction is not required", false, nil
	default:
		return "", false, fmt.Errorf("unknown CLI command: /%s (use /help)", name)
	}
}
func validateCLIModel(ctx context.Context, rt *runtime.Runtime, provider sdk.ProviderID, model string) error {
	if provider == "" {
		return errors.New("no provider selected")
	}
	models := rt.Router.Models(provider)
	if len(models) == 0 {
		if err := rt.RefreshProvider(ctx, provider); err != nil {
			return err
		}
		models = rt.Router.Models(provider)
	}
	for _, candidate := range models {
		if candidate.ID == model {
			return nil
		}
	}
	return fmt.Errorf("model %q is not available for provider %q", model, provider)
}
func cliHelp() string {
	return `Commands:
  /help                         Show this help
  /new, /clear                  Start a fresh session
  /sessions                     List saved sessions
  /resume [id]                  Resume a saved session
  /continue [id]                Alias for /resume
  /provider                     List providers
  /provider add <name> <adapter> <url> <api-key> [free]
  /models [provider]            List available models
  /model <model>                Select the current model
  /thinking <none|low|medium|high|off>
  /temperature <value|off>      Set sampling temperature
  /status, /details             Show current session settings
  /export                       Export current session to Markdown
  /compact                      Show context-window status
  /quit, /exit, /q              Exit the CLI

Controls:
  Up/Down        command history
  Left/Right     edit input
  Home/End       move cursor
  Ctrl+U         clear line
  Ctrl+K         delete to end
  Ctrl+C         cancel the current turn
  Ctrl+D         exit`
}
func currentModelState(ctx context.Context, sessions *runtime.SessionManager, id string) (string, string) {
	session, err := sessions.Resolve(ctx, sdk.Input{Source: clitransport.Source, SessionID: id})
	if err != nil {
		return "", ""
	}
	config := session.Config()
	return string(config.Provider), config.Model
}
func splitModel(value string) (string, string) {
	parts := strings.SplitN(value, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", value
}
func temperatureLabel(value *float64) string {
	if value == nil {
		return "auto"
	}
	return strconv.FormatFloat(*value, 'g', -1, 64)
}
func mustGetwd() string {
	value, err := os.Getwd()
	if err != nil {
		return "?"
	}
	return value
}
func newCLISessionID() string { return fmt.Sprintf("cli:%d", time.Now().UnixNano()) }
func joinProviderIDs(ids []sdk.ProviderID) string {
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, string(id))
	}
	return strings.Join(values, ", ")
}
func exportCLISession(ctx context.Context, sessions *runtime.SessionManager, id string) (string, bool, error) {
	session, err := sessions.Resolve(ctx, sdk.Input{Source: clitransport.Source, SessionID: id})
	if err != nil {
		return "", false, err
	}
	path := filepath.Join(".data", "transcripts", fmt.Sprintf("%s-%d.md", clitransport.ShortID(id), time.Now().Unix()))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", false, err
	}
	var b strings.Builder
	b.WriteString("# AI Harness Session\n\n")
	for _, turn := range session.History() {
		switch turn.Role {
		case sdk.RoleUser:
			b.WriteString("## User\n\n")
		case sdk.RoleModel:
			b.WriteString("## Assistant\n\n")
		case sdk.RoleToolCall:
			if turn.ToolCall != nil {
				b.WriteString(fmt.Sprintf("## Tool\n\n`%s` `%s`\n\n", turn.ToolCall.Name, clitransport.Compact(turn.ToolCall.Arguments, 900)))
			}
			continue
		case sdk.RoleToolResult:
			if turn.ToolResult != nil {
				b.WriteString(fmt.Sprintf("### Tool result%s\n\n%s\n\n", errorLabel(turn.ToolResult.IsError), turn.ToolResult.Content))
			}
			continue
		default:
			continue
		}
		for _, part := range turn.Content {
			if part.Type == sdk.ContentText {
				b.WriteString(part.Text)
			}
		}
		b.WriteString("\n\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", false, err
	}
	return "export › " + path, false, nil
}
func errorLabel(isError bool) string {
	if isError {
		return " (error)"
	}
	return ""
}
