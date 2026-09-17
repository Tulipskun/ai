package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tulipskun/ai/sdk"
)

const maxJobOutputBytes = 1 << 20

type JobState string

const (
	JobRunning     JobState = "running"
	JobCompleted   JobState = "completed"
	JobFailed      JobState = "failed"
	JobCancelled   JobState = "cancelled"
	JobInterrupted JobState = "interrupted"
)

type boundedBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.data) < maxJobOutputBytes {
		n := len(p)
		if len(b.data)+n > maxJobOutputBytes {
			n = maxJobOutputBytes - len(b.data)
		}
		b.data = append(b.data, p[:n]...)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(append([]byte(nil), b.data...))
}

type job struct {
	mu         sync.RWMutex
	id         string
	sessionID  string
	command    string
	args       []string
	cmd        *exec.Cmd
	state      JobState
	startedAt  time.Time
	finishedAt time.Time
	exitCode   int
	stdout     boundedBuffer
	stderr     boundedBuffer
	cancelled  bool
	err        string
	done       chan struct{}
}

type JobManager struct {
	workspace string
	statePath string
	mu        sync.RWMutex
	next      uint64
	jobs      map[string]*job
	persistMu sync.Mutex
}

type jobFile struct {
	Version int         `json:"version"`
	Jobs    []jobResult `json:"jobs"`
}

func NewJobManager(workspace, statePath string) *JobManager {
	m := &JobManager{workspace: workspace, statePath: statePath, jobs: make(map[string]*job)}
	m.load()
	return m
}

func (m *JobManager) load() {
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		return
	}
	var file jobFile
	if json.Unmarshal(data, &file) != nil || file.Version == 0 {
		return
	}
	for _, r := range file.Jobs {
		j := &job{id: r.ID, sessionID: r.SessionID, command: r.Command, args: append([]string(nil), r.Args...), state: r.Status, startedAt: r.StartedAt, exitCode: r.ExitCode, err: r.Error, done: make(chan struct{})}
		if r.Output != "" {
			j.stdout.data = append([]byte(nil), r.Output...)
		}
		if r.FinishedAt != nil {
			j.finishedAt = *r.FinishedAt
		}
		// Graceful-update handoff (REQ-043): a job still marked running when
		// the daemon restarts was in flight across the binary replace, not a
		// command failure. Keep its command/args/session/output persisted and
		// mark it interrupted (retryable) instead of failed so the new daemon
		// resumes with the record intact.
		if j.state == JobRunning {
			j.state = JobInterrupted
			j.err = "daemon restarted during update; job did not complete"
			j.finishedAt = time.Now()
			if j.exitCode == 0 {
				j.exitCode = -1
			}
		}
		close(j.done)
		m.jobs[j.id] = j
		if strings.HasPrefix(j.id, "job-") {
			if n, err := strconv.ParseUint(strings.TrimPrefix(j.id, "job-"), 10, 64); err == nil && n > m.next {
				m.next = n
			}
		}
	}
}

func (m *JobManager) persist() error {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	m.mu.RLock()
	records := make([]jobResult, 0, len(m.jobs))
	for _, j := range m.jobs {
		records = append(records, snapshotJob(j))
	}
	m.mu.RUnlock()
	data, err := json.MarshalIndent(jobFile{Version: 1, Jobs: records}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(m.statePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "jobs-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, m.statePath)
}

func (m *JobManager) Start(sessionID, command string, args []string) (string, error) {
	return m.StartIn(sessionID, m.workspace, command, args)
}

func (m *JobManager) StartIn(sessionID, workspace, command string, args []string) (string, error) {
	if strings.TrimSpace(sessionID) == "" {
		return "", errors.New("session_id is required")
	}
	if command == "" {
		return "", errors.New("command is required")
	}
	cmd := exec.Command(command, args...)
	cmd.Dir = workspace
	j := &job{sessionID: sessionID, command: command, args: append([]string(nil), args...), cmd: cmd, state: JobRunning, startedAt: time.Now(), exitCode: -1, done: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = &j.stdout, &j.stderr
	if err := cmd.Start(); err != nil {
		return "", err
	}
	m.mu.Lock()
	m.next++
	j.id = fmt.Sprintf("job-%d", m.next)
	m.jobs[j.id] = j
	m.mu.Unlock()
	if err := m.persist(); err != nil {
		_ = cmd.Process.Kill()
		m.mu.Lock()
		delete(m.jobs, j.id)
		m.mu.Unlock()
		return "", fmt.Errorf("persist job: %w", err)
	}
	go m.wait(j)
	return j.id, nil
}

func (m *JobManager) wait(j *job) {
	defer close(j.done)
	err := j.cmd.Wait()
	j.mu.Lock()
	j.finishedAt = time.Now()
	if j.cancelled {
		j.state = JobCancelled
	} else if err != nil {
		j.state = JobFailed
		j.err = err.Error()
		if exitErr, ok := err.(*exec.ExitError); ok {
			j.exitCode = exitErr.ExitCode()
		}
	} else {
		j.state = JobCompleted
		j.exitCode = 0
	}
	j.mu.Unlock()
	_ = m.persist()
}

func (m *JobManager) Get(id string) (*job, error) {
	m.mu.RLock()
	j, ok := m.jobs[id]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown job: %s", id)
	}
	return j, nil
}

func (m *JobManager) Wait(id string) error {
	j, err := m.Get(id)
	if err != nil {
		return err
	}
	<-j.done
	return nil
}

func (m *JobManager) GetForSession(sessionID, id string) (*job, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("session_id is required")
	}
	j, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	j.mu.RLock()
	owner := j.sessionID
	j.mu.RUnlock()
	if owner != sessionID {
		return nil, fmt.Errorf("job %s does not belong to session %s", id, sessionID)
	}
	return j, nil
}

func (m *JobManager) CloseForSession(sessionID, id string) error {
	j, err := m.GetForSession(sessionID, id)
	if err != nil {
		return err
	}
	j.mu.Lock()
	if j.state != JobRunning {
		j.mu.Unlock()
		return nil
	}
	j.cancelled = true
	process := j.cmd.Process
	done := j.done
	j.mu.Unlock()
	if process == nil {
		return nil
	}
	if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	<-done
	return m.persist()
}

type jobResult struct {
	ID         string     `json:"id"`
	SessionID  string     `json:"session_id"`
	Status     JobState   `json:"status"`
	Command    string     `json:"command"`
	Args       []string   `json:"args,omitempty"`
	Output     string     `json:"output"`
	Error      string     `json:"error,omitempty"`
	ExitCode   int        `json:"exit_code"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func snapshotJob(j *job) jobResult {
	j.mu.RLock()
	defer j.mu.RUnlock()
	r := jobResult{ID: j.id, SessionID: j.sessionID, Status: j.state, Command: j.command, Args: append([]string(nil), j.args...), Output: j.stdout.String(), Error: j.err, ExitCode: j.exitCode, StartedAt: j.startedAt}
	if !j.finishedAt.IsZero() {
		v := j.finishedAt
		r.FinishedAt = &v
	}
	return r
}

func encodeJob(j *job) (string, error) {
	data, err := json.Marshal(snapshotJob(j))
	return string(data), err
}

func sessionIDFromToolContext(ctx context.Context) (string, error) {
	id := sdk.SessionIDFromContext(ctx)
	if id == "" {
		return "", errors.New("session_id is required for job tools")
	}
	return id, nil
}

func runJobTool(m *JobManager, rootAt func(context.Context) string) handler {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var a struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		sessionID, err := sessionIDFromToolContext(ctx)
		if err != nil {
			return "", err
		}
		id, err := m.StartIn(sessionID, rootAt(ctx), a.Command, a.Args)
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(map[string]string{"job_id": id})
		return string(data), err
	}
}

func checkJobTool(m *JobManager) handler {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var a struct {
			JobID string `json:"job_id"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		sessionID, err := sessionIDFromToolContext(ctx)
		if err != nil {
			return "", err
		}
		j, err := m.GetForSession(sessionID, a.JobID)
		if err != nil {
			return "", err
		}
		return encodeJob(j)
	}
}

func closeJobTool(m *JobManager) handler {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var a struct {
			JobID string `json:"job_id"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		sessionID, err := sessionIDFromToolContext(ctx)
		if err != nil {
			return "", err
		}
		if err := m.CloseForSession(sessionID, a.JobID); err != nil {
			return "", err
		}
		j, err := m.GetForSession(sessionID, a.JobID)
		if err != nil {
			return "", err
		}
		return encodeJob(j)
	}
}
