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
