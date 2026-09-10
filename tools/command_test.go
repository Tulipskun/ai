package tools

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"time"
)

func TestRunCommandTimeoutKillsChildProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group behavior is platform-specific")
	}

	ctx := context.Background()
	raw, err := json.Marshal(runCommandArgs{
		Command:  "sh",
		Args:     []string{"-c", "sleep 5"},
		TimeoutMS: 100,
	})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err = runCommandTool(t.TempDir())(ctx, raw)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected command timeout")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("run_command did not return promptly after timeout: %s", elapsed)
	}
}

func TestHasShellSyntax(t *testing.T) {
	chained := []string{"echo a && echo b", "a || b", "a | b", "a; b", "echo x > f", "cat < f", "echo $HOME", "echo `date`", "ls *.go", "a\nb"}
	for _, line := range chained {
		if !hasShellSyntax(line) {
			t.Fatalf("expected shell syntax: %q", line)
		}
	}
	plain := []string{"echo", "ls -la", "go test ./...", "grep -r pattern dir"}
	for _, line := range plain {
		if hasShellSyntax(line) {
			t.Fatalf("unexpected shell syntax: %q", line)
		}
	}
}

func runCommandOutput(t *testing.T, workspace, command string, args ...string) commandOutput {
	t.Helper()
	raw, err := json.Marshal(runCommandArgs{Command: command, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	data, err := runCommandTool(workspace)(context.Background(), raw)
	if err != nil {
		t.Fatalf("run %q: %v (output %s)", command, err, data)
	}
	var out commandOutput
	if err := json.Unmarshal([]byte(data), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRunCommandChain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell chain syntax is platform-specific")
	}
	out := runCommandOutput(t, t.TempDir(), "echo chain-a && echo chain-b")
	if out.ExitCode != 0 || out.Output != "chain-a\nchain-b" {
		t.Fatalf("unexpected chain result: %+v", out)
	}
}

func TestRunCommandPipe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell pipe syntax is platform-specific")
	}
	out := runCommandOutput(t, t.TempDir(), "echo hello | tr a-z A-Z")
	if out.ExitCode != 0 || out.Output != "HELLO" {
		t.Fatalf("unexpected pipe result: %+v", out)
	}
}

func TestRunCommandRedirect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell redirect syntax is platform-specific")
	}
	out := runCommandOutput(t, t.TempDir(), "echo data > out.txt && cat out.txt")
	if out.ExitCode != 0 || out.Output != "data" {
		t.Fatalf("unexpected redirect result: %+v", out)
	}
}

func TestRunCommandArgsStayDirect(t *testing.T) {
	out := runCommandOutput(t, t.TempDir(), "echo", "a && b")
	if out.ExitCode != 0 || out.Output != "a && b" {
		t.Fatalf("args with operators must stay literal: %+v", out)
	}
}
