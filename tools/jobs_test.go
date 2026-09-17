package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tulipskun/ai/sdk"
)

func TestBackgroundJobLifecycle(t *testing.T) {
	root := t.TempDir()
	m := NewJobManager(root, root+"/jobs.json")
	id, err := m.Start("session-a", "sh", []string{"-c", "printf done"})
	if err != nil {
		t.Fatal(err)
	}
	var state JobState
	for i := 0; i < 50; i++ {
		j, _ := m.Get(id)
		state = snapshotJob(j).Status
		if state != JobRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if state != JobCompleted {
		t.Fatalf("state=%s", state)
	}
	j, _ := m.Get(id)
	if !strings.Contains(snapshotJob(j).Output, "done") {
		t.Fatalf("output=%q", snapshotJob(j).Output)
	}
}

func TestCloseBackgroundJob(t *testing.T) {
	root := t.TempDir()
	m := NewJobManager(root, root+"/jobs.json")
	id, err := m.Start("session-a", "sh", []string{"-c", "sleep 5"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CloseForSession("session-a", id); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		j, _ := m.Get(id)
		if snapshotJob(j).Status == JobCancelled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	j, _ := m.Get(id)
	t.Fatalf("state=%s", snapshotJob(j).Status)
}

func TestSessionOwnership(t *testing.T) {
	root := t.TempDir()
	m := NewJobManager(root, root+"/jobs.json")
	id, err := m.Start("session-a", "sh", []string{"-c", "sleep 1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetForSession("session-b", id); err == nil {
		t.Fatal("expected ownership error")
	}
	if err := m.CloseForSession("session-b", id); err == nil {
		t.Fatal("expected ownership error")
	}
	if err := m.CloseForSession("session-a", id); err != nil {
		t.Fatal(err)
	}
}

func TestJobPersistenceStoresSession(t *testing.T) {
	root := t.TempDir()
	statePath := root + "/jobs.json"
	m := NewJobManager(root, statePath)
	id, err := m.Start("session-a", "sh", []string{"-c", "printf ok"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		j, _ := m.Get(id)
		if snapshotJob(j).Status != JobRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var file jobFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Jobs) != 1 || file.Jobs[0].SessionID != "session-a" {
		t.Fatalf("jobs=%+v", file.Jobs)
	}
}

func TestRegistryJobToolsRequireSession(t *testing.T) {
	root := t.TempDir()
	r, err := NewRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	res := r.Execute(context.Background(), sdkCall("run_job", map[string]any{"command": "sh", "args": []string{"-c", "printf ok"}}))
	if !res.IsError {
		t.Fatal("expected missing session error")
	}
	ctx := sdk.WithSessionID(context.Background(), "session-a")
	call := sdk.ToolCall{Name: "run_job", Arguments: `{"command":"sh","args":["-c","printf ok"]}`}
	fn := r.handlers[call.Name]
	content, err := fn(ctx, json.RawMessage(call.Arguments))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["job_id"] == "" {
		t.Fatal("missing job id")
	}
	if err := r.jobs.Wait(payload["job_id"]); err != nil {
		t.Fatal(err)
	}
	j, _ := r.jobs.Get(payload["job_id"])
	if j == nil || snapshotJob(j).Status == JobRunning {
		t.Fatal("job still running")
	}
}

func TestInterruptedJobsPreservedAcrossRestart(t *testing.T) {
	root := t.TempDir()
	statePath := root + "/jobs.json"
	payload := map[string]any{"version": 1, "jobs": []any{
		map[string]any{"id": "job-7", "session_id": "session-a", "status": "running", "command": "sh", "args": []string{"-c", "sleep 30"}, "output": "partial", "exit_code": -1, "started_at": "2026-09-17T00:00:00Z"},
		map[string]any{"id": "job-8", "session_id": "session-a", "status": "completed", "command": "sh", "output": "done", "exit_code": 0, "started_at": "2026-09-17T00:00:00Z"},
	}}
	data, _ := json.Marshal(payload)
	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewJobManager(root, statePath)
	j, err := m.Get("job-7")
	if err != nil {
		t.Fatal(err)
	}
	snap := snapshotJob(j)
	if snap.Status != JobInterrupted {
		t.Fatalf("in-flight job status=%s, want interrupted", snap.Status)
	}
	if snap.Command != "sh" || snap.SessionID != "session-a" || snap.Output != "partial" {
		t.Fatalf("in-flight job lost record: %+v", snap)
	}
	j2, err := m.Get("job-8")
	if err != nil {
		t.Fatal(err)
	}
	if snapshotJob(j2).Status != JobCompleted {
		t.Fatalf("completed job changed: %s", snapshotJob(j2).Status)
	}
}
