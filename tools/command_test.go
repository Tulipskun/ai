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
