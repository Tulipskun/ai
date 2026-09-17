package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseUpdateArgs(t *testing.T) {
	v, auto, err := parseUpdateArgs(nil)
	if err != nil || v != "" || auto {
		t.Fatalf("defaults = %q,%v,%v", v, auto, err)
	}
	v, auto, err = parseUpdateArgs([]string{"v1.2.3"})
	if err != nil || v != "v1.2.3" || auto {
		t.Fatalf("version = %q,%v,%v", v, auto, err)
	}
	v, auto, err = parseUpdateArgs([]string{"--auto"})
	if err != nil || v != "" || !auto {
		t.Fatalf("auto = %q,%v,%v", v, auto, err)
	}
	v, auto, err = parseUpdateArgs([]string{"--auto", "v1.2.3"})
	if err != nil || v != "v1.2.3" || !auto {
		t.Fatalf("auto+version = %q,%v,%v", v, auto, err)
	}
	if _, _, err := parseUpdateArgs([]string{"v1", "v2"}); err == nil {
		t.Fatal("expected error for two versions")
	}
	if _, _, err := parseUpdateArgs([]string{"--bogus"}); err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestUpdateHandoffRoundTrip(t *testing.T) {
	root := t.TempDir()
	h := updateHandoff{OldVersion: "v1", NewVersion: "v2", OldHash: "a", NewHash: "b", PID: 123, Drained: true}
	if err := writeUpdateHandoff(root, h); err != nil {
		t.Fatal(err)
	}
	got, err := readUpdateHandoff(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.OldVersion != "v1" || got.NewVersion != "v2" || got.PID != 123 || !got.Drained {
		t.Fatalf("handoff=%+v", got)
	}
	if got.At == "" {
		t.Fatal("handoff At not stamped")
	}
	clearUpdateHandoff(root)
	if _, err := readUpdateHandoff(root); err == nil {
		t.Fatal("expected missing handoff after clear")
	}
}

func TestCountRunningJobsFile(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "missing.json")
	if n := countRunningJobsFile(empty); n != 0 {
		t.Fatalf("missing=%d", n)
	}
	path := filepath.Join(root, "jobs.json")
	payload := map[string]any{"version": 1, "jobs": []any{
		map[string]any{"id": "job-1", "status": "running"},
		map[string]any{"id": "job-2", "status": "completed"},
	}}
	data, _ := json.Marshal(payload)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if n := countRunningJobsFile(path); n != 1 {
		t.Fatalf("running=%d", n)
	}
	if waitForJobsDrain(path, 0) {
		t.Fatal("expected undrained when a job is still running")
	}
	done := filepath.Join(root, "done.json")
	payload2 := map[string]any{"version": 1, "jobs": []any{
		map[string]any{"id": "job-1", "status": "completed"},
	}}
	data2, _ := json.Marshal(payload2)
	if err := os.WriteFile(done, data2, 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitForJobsDrain(done, time.Second) {
		t.Fatal("expected drained when no running jobs")
	}
}

func TestStopDaemonForUpdateWhenNotRunning(t *testing.T) {
	if daemonRunning() {
		t.Skip("daemon is running; must not stop it from a unit test")
	}
	running, err := stopDaemonForUpdate(time.Second)
	if err != nil {
		t.Fatalf("stop when idle should not fail: %v", err)
	}
	if running {
		t.Fatal("no daemon should be reported when pid file is absent or stale")
	}
}

func TestVerifyDaemonHealthyTimeout(t *testing.T) {
	if err := verifyDaemonHealthy(300 * time.Millisecond); err == nil {
		t.Skip("a daemon is running in this environment; health check passed")
	}
}
