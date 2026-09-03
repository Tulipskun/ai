package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnvLoadsValuesWithoutOverridingExistingEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("DISCORD_BOT_TOKEN=file-token\nAI_MAX_OUTPUT_TOKENS=4096\n# comment\n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DISCORD_BOT_TOKEN", "existing-token")
	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}

	if got := os.Getenv("DISCORD_BOT_TOKEN"); got != "existing-token" {
		t.Fatalf("DISCORD_BOT_TOKEN=%q, want existing environment value", got)
	}
	if got := os.Getenv("AI_MAX_OUTPUT_TOKENS"); got != "4096" {
		t.Fatalf("AI_MAX_OUTPUT_TOKENS=%q, want %q", got, "4096")
	}
}

func TestLoadDotEnvAllowsQuotedValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("AI_SYSTEM_PROMPT=\"hello world\"\nAI_WORKSPACE='workspace path'\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("AI_SYSTEM_PROMPT"); got != "hello world" {
		t.Fatalf("AI_SYSTEM_PROMPT=%q, want %q", got, "hello world")
	}
	if got := os.Getenv("AI_WORKSPACE"); got != "workspace path" {
		t.Fatalf("AI_WORKSPACE=%q, want %q", got, "workspace path")
	}
}
