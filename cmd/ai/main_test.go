package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func TestNewAgentWithTools(t *testing.T) {
	client := sdk.NewRouterClient(sdk.NewRouter())
	agent, err := newAgent(client, t.TempDir(), nil, false, filepath.Join(t.TempDir(), "jobs.json"))
	if err != nil { t.Fatal(err) }
	if agent == nil { t.Fatal("agent is nil") }
	if agent.Client != client { t.Fatal("agent client was not wired") }
	if agent.Tools == nil { t.Fatal("agent tools are not wired") }
	if len(agent.Tools.Definitions()) == 0 { t.Fatal("agent has no tool definitions") }
}

func TestStateRootUsesAIDataDir(t *testing.T) {
	t.Setenv("AI_DATA_DIR", filepath.Join(t.TempDir(), "state"))
	got, err := stateRoot()
	if err != nil { t.Fatal(err) }
	want := filepath.Join(os.Getenv("AI_DATA_DIR"))
	if got != want { t.Fatalf("stateRoot = %q, want %q", got, want) }
}

func TestResolveWorkspaceDefaultsToHome(t *testing.T) {
	t.Setenv("AI_WORKSPACE", "")
	home, err := os.UserHomeDir()
	if err != nil || home == "" { t.Skip("no home directory") }
	got, err := resolveWorkspace()
	if err != nil { t.Fatal(err) }
	if got != home { t.Fatalf("resolveWorkspace = %q, want home %q", got, home) }
}

func TestResolveWorkspaceCreatesCustomDir(t *testing.T) {
	target := filepath.Join(t.TempDir(), "my-project")
	t.Setenv("AI_WORKSPACE", target)
	got, err := resolveWorkspace()
	if err != nil { t.Fatal(err) }
	if got != target { t.Fatalf("resolveWorkspace = %q, want %q", got, target) }
	if info, err := os.Stat(target); err != nil || !info.IsDir() { t.Fatalf("workspace dir was not created: %v", err) }
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" { t.Skip("no home directory") }
	if got := expandHome("~"); got != home { t.Fatalf("expandHome(~) = %q, want %q", got, home) }
	if got := expandHome("~/my-project"); got != filepath.Join(home, "my-project") { t.Fatalf("expandHome(~/my-project) = %q", got) }
	if got := expandHome("/tmp/x"); got != "/tmp/x" { t.Fatalf("expandHome(/tmp/x) = %q", got) }
}
