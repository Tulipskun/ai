package main

import (
	"strings"
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func TestSplitModel(t *testing.T) {
	provider, model := splitModel("openai/gpt-test")
	if provider != "openai" || model != "gpt-test" {
		t.Fatalf("splitModel = %q/%q", provider, model)
	}
	provider, model = splitModel("gpt-test")
	if provider != "" || model != "gpt-test" {
		t.Fatalf("splitModel without provider = %q/%q", provider, model)
	}
}

func TestJoinProviderIDs(t *testing.T) {
	got := joinProviderIDs([]sdk.ProviderID{"openai", "gemini"})
	if got != "openai, gemini" {
		t.Fatalf("joinProviderIDs = %q", got)
	}
}

func TestCLIHelpContainsCoreCommands(t *testing.T) {
	help := cliHelp()
	for _, command := range []string{"/help", "/new", "/sessions", "/resume", "/provider", "/models", "/model", "/thinking", "/temperature", "/status", "/export", "/quit"} {
		if !strings.Contains(help, command) {
			t.Fatalf("help missing %s", command)
		}
	}
}
