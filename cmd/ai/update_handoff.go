package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Graceful self-update handoff (REQ-043).
//
// `ai update` replaces the installed binary and restarts the daemon. An
// abrupt SIGKILL would drop in-flight turns and orphan background-job
// bookkeeping, so the update path drains first: it waits (with a timeout)
// for queued background jobs to finish, SIGTERMs the daemon to let the
// current turn settle, falls back to SIGKILL only when the daemon does not
// exit, then verifies the new daemon is healthy. A small handoff file under
// the state root records the old/new version and hashes so the new daemon
// can log the resume on boot.

const updateHandoffFileName = "update.handoff.json"

// updateDrainTimeout bounds the whole graceful stop (jobs drain + daemon
// exit) before the SIGKILL fallback. updateJobsDrainTimeout bounds only the
// pre-SIGTERM wait for background jobs recorded in data/jobs.json.
// updateHealthTimeout bounds the post-restart liveness poll.
const (
	updateDrainTimeout     = 30 * time.Second
	updateJobsDrainTimeout = 15 * time.Second
	updateHealthTimeout    = 30 * time.Second
)

// updateHandoff is the on-disk resume signal the new daemon consumes.
type updateHandoff struct {
	OldVersion string `json:"old_version"`
	NewVersion string `json:"new_version"`
	OldHash    string `json:"old_hash"`
	NewHash    string `json:"new_hash"`
	PID        int    `json:"pid"`
	At         string `json:"at"`
	Drained    bool   `json:"drained"`
}

// parseUpdateArgs accepts `ai update [--auto] [version]`. --auto marks a
// non-interactive self-check run; behavior is identical today (the command
// never prompts) but the flag is reserved for daemon-driven checks.
func parseUpdateArgs(args []string) (version string, auto bool, err error) {
	for _, a := range args {
		if a == "--auto" {
			auto = true
			continue
		}
		if strings.HasPrefix(a, "-") {
			return "", false, fmt.Errorf("update: usage is 'ai update [--auto] [version]'")
		}
		if version != "" {
			return "", false, fmt.Errorf("update: usage is 'ai update [--auto] [version]'")
		}
		version = strings.TrimSpace(a)
	}
	return version, auto, nil
}

func updateHandoffPathForRoot(root string) string {
	return filepath.Join(root, updateHandoffFileName)
}

func writeUpdateHandoff(root string, h updateHandoff) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("state root is required")
	}
	if strings.TrimSpace(h.At) == "" {
		h.At = time.Now().UTC().Format(time.RFC3339Nano)
	}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(updateHandoffPathForRoot(root), data, 0o644)
}

func readUpdateHandoff(root string) (updateHandoff, error) {
	var h updateHandoff
	data, err := os.ReadFile(updateHandoffPathForRoot(root))
	if err != nil {
		return h, err
	}
	if err := json.Unmarshal(data, &h); err != nil {
		return updateHandoff{}, err
	}
	return h, nil
}

func clearUpdateHandoff(root string) {
	_ = os.Remove(updateHandoffPathForRoot(root))
}

func currentDaemonPID(root string) int {
	if strings.TrimSpace(root) == "" {
		return 0
	}
	data, err := os.ReadFile(filepath.Join(root, "ai.pid"))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// countRunningJobsFile reports how many jobs.json entries are still running.
// A missing or unreadable file means zero: there is nothing to drain.
func countRunningJobsFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return 0
	}
	var file struct {
		Jobs []struct {
			Status string `json:"status"`
		} `json:"jobs"`
	}
	if json.Unmarshal(data, &file) != nil {
		return 0
	}
	n := 0
	for _, j := range file.Jobs {
		if j.Status == "running" {
			n++
		}
	}
	return n
}

// waitForJobsDrain polls jobs.json until no entry is running or the timeout
// elapses. It never fails the update; a timeout simply means the daemon is
// stopped with jobs still in flight (their commands stay persisted in
// jobs.json for retry).
func waitForJobsDrain(jobsPath string, timeout time.Duration) bool {
	if timeout <= 0 {
		return countRunningJobsFile(jobsPath) == 0
	}
	deadline := time.Now().Add(timeout)
	for {
		if countRunningJobsFile(jobsPath) == 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// stopDaemonForUpdate gracefully stops the daemon for a binary replace: wait
// briefly for background jobs to drain, SIGTERM the daemon so the current
// turn can settle, then SIGKILL only when it does not exit within
// drainTimeout. It reports whether a daemon was running.
func stopDaemonForUpdate(drainTimeout time.Duration) (bool, error) {
	root, err := stateRoot()
	if err != nil {
		return false, err
	}
	pidPath := filepath.Join(root, "ai.pid")
	pid := currentDaemonPID(root)
	if pid <= 0 || !processAlive(pid) {
		_ = os.Remove(pidPath)
		return false, nil
	}
	if drainTimeout <= 0 {
		drainTimeout = updateDrainTimeout
	}
	jobsTimeout := updateJobsDrainTimeout
	if jobsTimeout > drainTimeout {
		jobsTimeout = drainTimeout / 2
	}
	waitForJobsDrain(filepath.Join(root, "data", "jobs.json"), jobsTimeout)
	killBrowserChild(root)
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if pid2 := currentDaemonPID(root); pid2 <= 0 || !processAlive(pid2) {
			_ = os.Remove(pidPath)
			return true, nil
		}
	}
	deadline := time.Now().Add(drainTimeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			_ = os.Remove(pidPath)
			return true, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if processAlive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	_ = os.Remove(pidPath)
	return true, nil
}

// verifyDaemonHealthy polls until ai.pid points at a live process.
func verifyDaemonHealthy(timeout time.Duration) error {
	if timeout <= 0 {
		timeout = updateHealthTimeout
	}
	deadline := time.Now().Add(timeout)
	for {
		if daemonRunning() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("new daemon not healthy within %s", timeout)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// consumeUpdateHandoff logs the resume signal left by `ai update` and
// removes it. Called once on daemon boot; missing or corrupt files are
// ignored so normal boots stay silent.
func consumeUpdateHandoff(state string) {
	if strings.TrimSpace(state) == "" {
		return
	}
	h, err := readUpdateHandoff(state)
	if err != nil {
		return
	}
	log.Printf("update handoff resumed old=%s new=%s pid=%d at=%s drained=%t",
		h.OldVersion, h.NewVersion, h.PID, h.At, h.Drained)
	_ = os.Remove(updateHandoffPathForRoot(state))
}
