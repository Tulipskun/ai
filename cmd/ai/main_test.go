package main

import (
	"os"
	"path/filepath"
	"strings"
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

func TestStateRootDefaultsToLocalShare(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" { t.Skip("no home directory") }
	got, err := stateRoot()
	if err != nil { t.Fatal(err) }
	want := filepath.Join(home, ".local", "share", "ai")
	if got != want { t.Fatalf("stateRoot = %q, want %q", got, want) }
}

func TestResolveWorkspaceDefaultsToHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" { t.Skip("no home directory") }
	got, err := resolveWorkspace("")
	if err != nil { t.Fatal(err) }
	if got != home { t.Fatalf("resolveWorkspace = %q, want home %q", got, home) }
}

func TestResolveWorkspaceCreatesConfiguredDir(t *testing.T) {
	target := filepath.Join(t.TempDir(), "my-project")
	got, err := resolveWorkspace(target)
	if err != nil { t.Fatal(err) }
	if got != target { t.Fatalf("resolveWorkspace = %q, want %q", got, target) }
	if info, err := os.Stat(target); err != nil || !info.IsDir() { t.Fatalf("workspace dir was not created: %v", err) }
}

func TestResolveWorkspaceExpandsHomePrefix(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" { t.Skip("no home directory") }
	got, err := resolveWorkspace("~/my-project")
	if err != nil { t.Fatal(err) }
	if got != filepath.Join(home, "my-project") { t.Fatalf("resolveWorkspace = %q", got) }
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" { t.Skip("no home directory") }
	if got := expandHome("~"); got != home { t.Fatalf("expandHome(~) = %q, want %q", got, home) }
	if got := expandHome("~/my-project"); got != filepath.Join(home, "my-project") { t.Fatalf("expandHome(~/my-project) = %q", got) }
	if got := expandHome("/tmp/x"); got != "/tmp/x" { t.Fatalf("expandHome(/tmp/x) = %q", got) }
}

func TestSystemPromptDefaultMentionsTools(t *testing.T) {
	client := sdk.NewRouterClient(sdk.NewRouter())
	agent, err := newAgent(client, t.TempDir(), nil, false, "")
	if err != nil { t.Fatal(err) }
	got := systemPromptWithState(t.TempDir(), agent)
	for _, want := range []string{"CALL the matching tool", "run_command", "read_file", "Available tools:"} {
		if !containsStr(got, want) {
			t.Fatalf("default prompt missing %q:\n%s", want, got)
		}
	}
}

func TestSystemPromptNilAgentHasRules(t *testing.T) {
	got := systemPromptWithState(t.TempDir(), nil)
	if !containsStr(got, "gets things done with tools") {
		t.Fatalf("default prompt missing rules:\n%s", got)
	}
}

func TestSystemPromptMentionsPlanLifecycle(t *testing.T) {
	got := systemPromptWithState(t.TempDir(), nil)
	for _, want := range []string{"plan_create", "plan_check", "plan_update", "plan_close"} {
		if !containsStr(got, want) {
			t.Fatalf("default prompt missing %q:\n%s", want, got)
		}
	}
}

func containsStr(haystack, needle string) bool { return strings.Contains(haystack, needle) }

func TestSystemPromptFileOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config", "system.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(path, []byte(`{"system_prompt":"file prompt"}`), 0o600); err != nil { t.Fatal(err) }
	if got := systemPromptWithState(dir, nil); got != "file prompt" {
		t.Fatalf("systemPrompt = %q", got)
	}
	if src := systemPromptSourceWithState(dir); !containsStr(src, "system.json") {
		t.Fatalf("source = %q", src)
	}
}
