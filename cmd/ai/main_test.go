package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func TestNewAgentWithTools(t *testing.T) {
	client := sdk.NewRouterClient(sdk.NewRouter())
	agent, err := newAgent(client, t.TempDir(), nil, false, filepath.Join(t.TempDir(), "data", "jobs.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if agent == nil || agent.Client != client || agent.Tools == nil {
		t.Fatal("agent wiring is incomplete")
	}
}

func TestStateRootDefaultsToLocalShare(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	got, err := stateRoot()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".local", "share", "ai"); got != want {
		t.Fatalf("stateRoot = %q, want %q", got, want)
	}
}

func TestResolveWorkspaceDefaultsToHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	if got, err := resolveWorkspace(); err != nil || got != home {
		t.Fatalf("resolveWorkspace = %q, %v", got, err)
	}
}

func TestResolveWorkspaceConfiguredDir(t *testing.T) {
	target := filepath.Join(t.TempDir(), "my-project")
	if err := os.Setenv("AI_WORKSPACE", target); err != nil {
		t.Fatal(err)
	}
	defer os.Unsetenv("AI_WORKSPACE")
	got, err := resolveWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Fatalf("resolveWorkspace = %q, want %q", got, target)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("workspace dir was not created: %v", err)
	}
}

func TestDefaultSystemPromptUsesPlanAndSubagent(t *testing.T) {
	client := sdk.NewRouterClient(sdk.NewRouter())
	agent := &sdk.Agent{Client: client, Tools: &testPromptTools{}}
	got := defaultSystemPrompt(agent)
	for _, forbidden := range []string{"gets things done with tools", "CALL the matching tool", "Prefer acting first", "run_command", "read_file", "Available tools:", "call the tool in the SAME response", "Execute only the current step"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("main default contains execution instruction %q: %s", forbidden, got)
		}
	}
	for _, want := range []string{"Before creating the plan", "ordered execution plan", "delegate the current plan step to `delegate_to_subagent`", "Call `accept_subagent_result` with verification evidence before delegating the next step"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

type testPromptTools struct{}

func (testPromptTools) Definitions() []sdk.Tool {
	return []sdk.Tool{{Name: "run_command", Description: "run command"}}
}
func (testPromptTools) Execute(_ context.Context, _ sdk.ToolCall) sdk.ToolResult {
	return sdk.ToolResult{}
}

func TestDefaultPromptRequiresSameSessionRetryAndAcceptance(t *testing.T) {
	prompt := defaultSystemPrompt(nil)
	for _, name := range []string{"subagent_history", "subagent_status", "follow_up_subagent", "continue_subagent", "accept_subagent_result"} {
		if !strings.Contains(prompt, "`"+name+"`") {
			t.Fatalf("missing orchestration tool %s", name)
		}
	}
}
