package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Blue-green self-update (REQ-043, CHANGE-055): the ONLY update flow.
//
// A legacy path once stopped the blue daemon before the replacement binary
// was proven, leaving a zero-daemon window on a bad build. The blue-green
// path stages the verified binary to a temp path and starts it as a green
// standby in probation mode (shadow pid file ai.pid.green, shadow heartbeat
// discord.heartbeat.green, phase file update.bluegreen.json) while blue keeps
// serving. Only after all health gates pass within 60-90s is blue SIGTERMed
// (existing 30s drain semantics) and green promoted atomically. A failed
// green is rolled back (killed, shadows removed) with blue untouched, so there
// is never a zero-daemon window. When no daemon runs, the updater starts one
// first (ensureBlueRunning) so the handover still runs this same path.
//
// The single `ai` binary owns the handover end to end: stage, standby, gates,
// stop, promote, confirm, rollback. There is no external supervisor —
// scripts/keepalive.sh and scripts/supervisor.sh were removed in CHANGE-055.
// Cutover has one logical owner: the updater performs the atomic file
// operations while the standby idempotently re-asserts the same values (it
// makes no cutover decision itself).
//
// Hash verification, no-restart-if-unchanged, jobs drain (waitForJobsDrain),
// SIGTERM-settle with SIGKILL fallback (stopDaemonForUpdate), interrupted-job
// mapping, handoff logging, and post-restart health polling keep their
// existing behavior and are reused here, not reimplemented.

const (
	blueGreenPhaseFileName  = "update.bluegreen.json"
	greenPIDFileName        = "ai.pid.green"
	greenHeartbeatFileName  = "discord.heartbeat.green"
	liveHeartbeatFileName   = "discord.heartbeat"
	greenReadyMarker        = "blue-green green-ready"
	greenLiveMarker         = "blue-green live intake enabled"
	greenStandbyArg         = "--standby"
	blueGreenHealthTimeout  = 75 * time.Second
	blueGreenLiveTimeout    = 30 * time.Second
	blueGreenPhaseExpiry    = 15 * time.Minute
	shadowHeartbeatMaxAge   = 60 * time.Second
	liveHeartbeatConfirmAge = 5 * time.Minute
)

// blueGreenPhase tracks one blue-green handover from staging to cutover.
type blueGreenPhase struct {
	OldVersion   string `json:"old_version"`
	NewVersion   string `json:"new_version"`
	OldHash      string `json:"old_hash"`
	NewHash      string `json:"new_hash"`
	BluePID      int    `json:"blue_pid"`
	GreenPID     int    `json:"green_pid"`
	StagedBinary string `json:"staged_binary"`
	At           string `json:"at"`
	CutoverDone  bool   `json:"cutover_done"`
}

func blueGreenPhasePathForRoot(root string) string {
	return filepath.Join(root, blueGreenPhaseFileName)
}

func greenPIDPathForRoot(root string) string {
	return filepath.Join(root, greenPIDFileName)
}

func greenHeartbeatPathForRoot(root string) string {
	return filepath.Join(root, greenHeartbeatFileName)
}

func liveHeartbeatPathForRoot(root string) string {
	return filepath.Join(root, liveHeartbeatFileName)
}

func daemonLogPathForRoot(root string) string {
	return filepath.Join(root, "ai.log")
}

func writeBlueGreenPhase(root string, phase blueGreenPhase) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("state root is required")
	}
	if strings.TrimSpace(phase.At) == "" {
		phase.At = time.Now().UTC().Format(time.RFC3339Nano)
	}
	data, err := json.MarshalIndent(phase, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(blueGreenPhasePathForRoot(root), data, 0o644)
}

func readBlueGreenPhase(root string) (blueGreenPhase, error) {
	var phase blueGreenPhase
	data, err := os.ReadFile(blueGreenPhasePathForRoot(root))
	if err != nil {
		return phase, err
	}
	if err := json.Unmarshal(data, &phase); err != nil {
		return blueGreenPhase{}, err
	}
	return phase, nil
}

func clearBlueGreenPhase(root string) {
	_ = os.Remove(blueGreenPhasePathForRoot(root))
}

func blueGreenPhaseAge(root string, phase blueGreenPhase) time.Duration {
	if ts, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(phase.At)); err == nil {
		return time.Since(ts)
	}
	if fi, err := os.Stat(blueGreenPhasePathForRoot(root)); err == nil {
		return time.Since(fi.ModTime())
	}
	return blueGreenPhaseExpiry + time.Second
}

// isBlueGreenHandoverActive reports whether a green probation is in progress:
// phase file exists, cutover not done, and not expired. While it holds, the
// updater owns the handover end to end (no external supervisor exists).
func isBlueGreenHandoverActive(root string) bool {
	phase, err := readBlueGreenPhase(root)
	if err != nil {
		return false
	}
	if phase.CutoverDone {
		return false
	}
	if blueGreenPhaseAge(root, phase) > blueGreenPhaseExpiry {
		return false
	}
	return true
}

// currentGreenPID reads the shadow pid file without touching the live ai.pid.
func currentGreenPID(root string) int {
	if strings.TrimSpace(root) == "" {
		return 0
	}
	data, err := os.ReadFile(greenPIDPathForRoot(root))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

func fileFreshWithin(path string, maxAge time.Duration) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	if maxAge <= 0 {
		return true
	}
	return time.Since(fi.ModTime()) <= maxAge
}

// shadowHeartbeatFresh checks the green standby heartbeat mtime. During
// probation the standby touches this file itself (no Discord intake yet), so
// freshness proves the green process is alive and writing.
func shadowHeartbeatFresh(root string, maxAge time.Duration) bool {
	return fileFreshWithin(greenHeartbeatPathForRoot(root), maxAge)
}

// fileTailContains searches only the last tailBytes of a file so polling
// ai.log never loads the whole log into memory.
func fileTailContains(path, marker string, tailBytes int64) bool {
	if marker == "" {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() <= 0 {
		return false
	}
	size := fi.Size()
	if tailBytes <= 0 || tailBytes > size {
		tailBytes = size
	}
	const maxTail = 256 * 1024
	if tailBytes > maxTail {
		tailBytes = maxTail
	}
	buf := make([]byte, tailBytes)
	if _, err := f.ReadAt(buf, size-tailBytes); err != nil {
		return false
	}
	return strings.Contains(string(buf), marker)
}

func logContainsMarker(logPath, marker string) bool {
	return fileTailContains(logPath, marker, 128*1024)
}

// handoffConsumed is true once the green standby has booted and consumed
// update.handoff.json (consumeUpdateHandoff deletes it).
func handoffConsumed(root string) bool {
	if strings.TrimSpace(root) == "" {
		return true
	}
	_, err := os.Stat(updateHandoffPathForRoot(root))
	return os.IsNotExist(err)
}

// checkGreenHealth verifies every pre-cutover gate: green pid alive via
// kill-0, ready marker in the log tail, shadow heartbeat fresh within 60s,
// and the handoff consumed. All four must pass before blue is stopped.
func checkGreenHealth(root, logPath string) error {
	var problems []string
	if pid := currentGreenPID(root); pid <= 0 || !processAlive(pid) {
		problems = append(problems, fmt.Sprintf("green pid not alive (shadow %s)", greenPIDFileName))
	}
	if !logContainsMarker(logPath, greenReadyMarker) {
		problems = append(problems, fmt.Sprintf("green log missing ready marker %q", greenReadyMarker))
	}
	if !shadowHeartbeatFresh(root, shadowHeartbeatMaxAge) {
		problems = append(problems, fmt.Sprintf("shadow heartbeat %s not fresh within %s", greenHeartbeatFileName, shadowHeartbeatMaxAge))
	}
	if !handoffConsumed(root) {
		problems = append(problems, "update handoff not consumed by green")
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("green health gates failed: %s", strings.Join(problems, "; "))
}

// waitForGreenHealth polls the gates until they all pass or the timeout
// (60-90s) elapses.
func waitForGreenHealth(root, logPath string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = blueGreenHealthTimeout
	}
	deadline := time.Now().Add(timeout)
	var last error
	for {
		if err := checkGreenHealth(root, logPath); err == nil {
			return nil
		} else {
			last = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("green not healthy within %s: %v", timeout, last)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// waitForGreenLive confirms the cutover: live ai.pid points at a live
// process, the green logged live-intake enablement, and the live heartbeat is
// fresh (seeded from the shadow heartbeat at promotion, then refreshed by the
// gateway once Discord connects).
func waitForGreenLive(state, logPath string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = blueGreenLiveTimeout
	}
	deadline := time.Now().Add(timeout)
	last := fmt.Errorf("not ready")
	for {
		if daemonRunning() && logContainsMarker(logPath, greenLiveMarker) {
			if fileFreshWithin(liveHeartbeatPathForRoot(state), liveHeartbeatConfirmAge) {
				return nil
			}
			last = fmt.Errorf("live heartbeat not fresh")
		} else {
			last = fmt.Errorf("live pid or live marker not ready")
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("green live intake not confirmed within %s: %v", timeout, last)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// tailLog returns the last maxLines lines (within maxBytes) for rollback
// error reports.
func tailLog(path string, maxLines, maxBytes int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if maxBytes > 0 && len(data) > maxBytes {
		data = data[len(data)-maxBytes:]
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func killPIDGraceful(pid int, timeout time.Duration) {
	if pid <= 0 || !processAlive(pid) {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	if processAlive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// rollbackBlueGreen kills a failed green standby and removes its staged
// binary, shadow pid/heartbeat, phase file, and any stale handoff, while
// leaving the live ai.pid, the installed binary, and the blue daemon
// untouched. Blue keeps serving; there is never a zero-daemon window.
func rollbackBlueGreen(root string, phase blueGreenPhase, logPath, reason string) error {
	if phase.GreenPID > 0 && processAlive(phase.GreenPID) {
		killPIDGraceful(phase.GreenPID, 5*time.Second)
	} else if pid := currentGreenPID(root); pid > 0 && processAlive(pid) {
		killPIDGraceful(pid, 5*time.Second)
	}
	if strings.TrimSpace(phase.StagedBinary) != "" {
		_ = os.Remove(phase.StagedBinary)
	}
	_ = os.Remove(greenPIDPathForRoot(root))
	_ = os.Remove(greenHeartbeatPathForRoot(root))
	_ = os.Remove(updateHandoffPathForRoot(root))
	clearBlueGreenPhase(root)
	if tail := tailLog(logPath, 30, 32*1024); tail != "" {
		return fmt.Errorf("blue-green update aborted: %s; green rolled back, blue kept serving (log tail:\n%s)", reason, tail)
	}
	return fmt.Errorf("blue-green update aborted: %s; green rolled back, blue kept serving", reason)
}

// startGreenStandby launches the staged binary as `daemon --standby` while
// blue keeps serving. Output goes to the shared ai.log so the ready marker is
// visible to the health gates. The shadow pid file is written by the starter
// (child pid is known here); the standby re-asserts it on boot.
func startGreenStandby(stagedBinary, state string) (int, error) {
	if strings.TrimSpace(stagedBinary) == "" {
		return 0, fmt.Errorf("staged binary is required")
	}
	if strings.TrimSpace(state) == "" {
		return 0, fmt.Errorf("state root is required")
	}
	if err := os.MkdirAll(state, 0o755); err != nil {
		return 0, err
	}
	if pid := currentGreenPID(state); pid > 0 && processAlive(pid) {
		return 0, fmt.Errorf("green standby already running (pid %d)", pid)
	}
	_ = os.Remove(greenPIDPathForRoot(state))
	logFile, err := os.OpenFile(daemonLogPathForRoot(state), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(stagedBinary, "daemon", greenStandbyArg)
	cmd.Dir = state
	cmd.Env = os.Environ()
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return 0, err
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		_ = logFile.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Process.Release()
		return 0, fmt.Errorf("failed to start green standby: invalid pid %d", pid)
	}
	if err := os.WriteFile(greenPIDPathForRoot(state), []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		_ = cmd.Process.Kill()
		_ = logFile.Close()
		_ = cmd.Process.Release()
		return 0, err
	}
	_ = logFile.Close()
	_ = cmd.Process.Release()
	return pid, nil
}

// promoteGreenToLive runs only after blue has exited: it atomically replaces
// the installed binary with the staged one, promotes the shadow pid file to
// ai.pid, seeds the live heartbeat from the shadow heartbeat, and marks the
// phase cutover-done so the standby enables live intake. It refuses cutover
// while any live (blue) pid is still alive. Single logical owner: the updater
// performs these atomic file operations; the standby only re-asserts the
// same pid/heartbeat values idempotently when it observes cutover-done and
// makes no cutover decision itself.
func promoteGreenToLive(state, appPath string, phase blueGreenPhase) error {
	if strings.TrimSpace(state) == "" {
		return fmt.Errorf("state root is required")
	}
	if strings.TrimSpace(appPath) == "" {
		return fmt.Errorf("installed binary path is required")
	}
	if pid := currentDaemonPID(state); pid > 0 && processAlive(pid) {
		return fmt.Errorf("blue daemon still running (pid %d); refusing cutover", pid)
	}
	greenPID := phase.GreenPID
	if greenPID <= 0 {
		greenPID = currentGreenPID(state)
	}
	if greenPID <= 0 || !processAlive(greenPID) {
		return fmt.Errorf("green standby not alive; refusing cutover")
	}
	if strings.TrimSpace(phase.StagedBinary) == "" {
		return fmt.Errorf("staged binary missing from phase")
	}
	if _, err := os.Stat(phase.StagedBinary); err != nil {
		return fmt.Errorf("staged binary not found: %w", err)
	}
	if err := os.Rename(phase.StagedBinary, appPath); err != nil {
		return fmt.Errorf("promote staged binary: %w", err)
	}
	livePIDPath := filepath.Join(state, "ai.pid")
	if data, err := os.ReadFile(livePIDPath); err == nil {
		if pid, perr := strconv.Atoi(strings.TrimSpace(string(data))); perr != nil || pid <= 0 || !processAlive(pid) {
			_ = os.Remove(livePIDPath)
		} else if pid != greenPID {
			return fmt.Errorf("live pid file changed under cutover (pid %d)", pid)
		}
	}
	if err := os.Rename(greenPIDPathForRoot(state), livePIDPath); err != nil {
		return fmt.Errorf("promote green pid file: %w (binary promoted, green pid %d alive)", err, greenPID)
	}
	if data, err := os.ReadFile(greenHeartbeatPathForRoot(state)); err == nil {
		_ = os.WriteFile(liveHeartbeatPathForRoot(state), data, 0o644)
	} else {
		touchLiveHeartbeat(state)
	}
	if p, err := readBlueGreenPhase(state); err == nil {
		p.CutoverDone = true
		p.GreenPID = greenPID
		_ = writeBlueGreenPhase(state, p)
	} else {
		phase.CutoverDone = true
		phase.GreenPID = greenPID
		_ = writeBlueGreenPhase(state, phase)
	}
	return nil
}

// ensureBlueRunning starts the daemon when none runs so every update flows
// through the same blue-green handover (REQ-043, CHANGE-055): there is no
// legacy replace-and-start branch. A start failure aborts the update with a
// clear error instead of silently switching paths.
func ensureBlueRunning(app string) error {
	if daemonRunning() {
		return nil
	}
	if strings.TrimSpace(app) == "" {
		return fmt.Errorf("installed binary path is required")
	}
	fmt.Println("[ai] daemon not running; starting blue first")
	if err := startDaemon(app); err != nil {
		return fmt.Errorf("start blue daemon: %w", err)
	}
	if err := verifyDaemonHealthy(updateHealthTimeout); err != nil {
		return fmt.Errorf("blue daemon unhealthy: %w", err)
	}
	return nil
}

// emergencyPromoteLiveGreen recovers a cutover that failed after blue was
// stopped (REQ-043, CHANGE-055): instead of leaving a zero-daemon window,
// it promotes the staged green — healthy seconds ago — through the same
// atomic steps (binary rename, cutover-done mark) so the standby enables
// live intake itself. A binary rename that already happened is a no-op. It
// refuses when some other live process owns ai.pid (manual triage needed).
// The caller still confirms live intake afterwards.
func emergencyPromoteLiveGreen(state, appPath string, phase blueGreenPhase) error {
	if strings.TrimSpace(state) == "" {
		return fmt.Errorf("state root is required")
	}
	if strings.TrimSpace(appPath) == "" {
		return fmt.Errorf("installed binary path is required")
	}
	greenPID := phase.GreenPID
	if greenPID <= 0 {
		greenPID = currentGreenPID(state)
	}
	if greenPID <= 0 || !processAlive(greenPID) {
		return fmt.Errorf("green standby not alive; cannot recover")
	}
	if pid := currentDaemonPID(state); pid > 0 && processAlive(pid) && pid != greenPID {
		return fmt.Errorf("live pid file changed under cutover (pid %d); manual triage needed", pid)
	}
	if strings.TrimSpace(phase.StagedBinary) == "" {
		return fmt.Errorf("staged binary missing from phase")
	}
	if err := os.Rename(phase.StagedBinary, appPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("emergency promote staged binary: %w", err)
	}
	if p, err := readBlueGreenPhase(state); err == nil {
		p.CutoverDone = true
		p.GreenPID = greenPID
		_ = writeBlueGreenPhase(state, p)
	} else {
		phase.CutoverDone = true
		phase.GreenPID = greenPID
		_ = writeBlueGreenPhase(state, phase)
	}
	_ = os.Remove(greenPIDPathForRoot(state))
	return nil
}

// confirmGreenLive verifies live intake after a successful promote and
// finalizes the handover (shadow cleanup, phase clear, completion message).
func confirmGreenLive(state, logPath string, greenPID int, oldVersion, newVersion string) error {
	if err := waitForGreenLive(state, logPath, blueGreenLiveTimeout); err != nil {
		return fmt.Errorf("green promoted but live intake unconfirmed (green pid %d, state %s): %w", greenPID, state, err)
	}
	_ = os.Remove(greenHeartbeatPathForRoot(state))
	clearBlueGreenPhase(state)
	fmt.Printf("[ai] blue-green update complete green=%d %s -> %s\n", greenPID, oldVersion, newVersion)
	return nil
}

// runBlueGreenUpdate orchestrates the handover — the only update flow. It
// drains while blue serves, stages the binary, hands off to a green standby,
// enforces health gates, stops blue with existing drain semantics, promotes
// green, confirms live intake, and finalizes. Green failures roll back with
// blue untouched; a promote failure after blue stopped recovers through
// emergencyPromoteLiveGreen instead of leaving a zero-daemon window.
func runBlueGreenUpdate(app, state, oldVersion, newVersion, oldHash, newHash string, binary []byte, bluePID int) error {
	if strings.TrimSpace(app) == "" {
		return fmt.Errorf("installed binary path is required")
	}
	if strings.TrimSpace(state) == "" {
		return fmt.Errorf("state root is required for blue-green update")
	}
	// Drain while blue still serves; jobs finishing now never need a retry.
	drained := true
	if jobsPath := jobsFilePathForUpdate(state); jobsPath != "" {
		drained = waitForJobsDrain(jobsPath, updateJobsDrainTimeout)
	}
	// Stage the new binary beside the installed one (same filesystem, so the
	// cutover rename is atomic). Blue keeps running from the old binary.
	tmp, err := os.CreateTemp(filepath.Dir(app), ".ai-green-*")
	if err != nil {
		return err
	}
	staged := tmp.Name()
	stagedOK := false
	defer func() {
		if !stagedOK {
			_ = os.Remove(staged)
		}
	}()
	if _, err := tmp.Write(binary); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Handoff first so green consumes it on standby boot (proves the boot
	// path); phase tracks the handover for gates and cutover.
	if err := writeUpdateHandoff(state, updateHandoff{
		OldVersion: oldVersion,
		NewVersion: newVersion,
		OldHash:    oldHash,
		NewHash:    newHash,
		PID:        bluePID,
		Drained:    drained,
	}); err != nil {
		return err
	}
	phase := blueGreenPhase{
		OldVersion:   oldVersion,
		NewVersion:   newVersion,
		OldHash:      oldHash,
		NewHash:      newHash,
		BluePID:      bluePID,
		StagedBinary: staged,
	}
	if err := writeBlueGreenPhase(state, phase); err != nil {
		clearUpdateHandoff(state)
		return err
	}
	greenPID, err := startGreenStandby(staged, state)
	if err != nil {
		_ = os.Remove(updateHandoffPathForRoot(state))
		clearBlueGreenPhase(state)
		return fmt.Errorf("start green standby: %w; blue kept serving", err)
	}
	phase.GreenPID = greenPID
	_ = writeBlueGreenPhase(state, phase)
	logPath := daemonLogPathForRoot(state)
	if err := waitForGreenHealth(state, logPath, blueGreenHealthTimeout); err != nil {
		return rollbackBlueGreen(state, phase, logPath, err.Error())
	}
	// Final drain right before cutover, then SIGTERM blue with the existing
	// 30s settle semantics. Blue is stopped only now that green is healthy.
	if jobsPath := jobsFilePathForUpdate(state); jobsPath != "" {
		waitForJobsDrain(jobsPath, updateJobsDrainTimeout)
	}
	if _, err := stopDaemonForUpdate(updateDrainTimeout); err != nil {
		return rollbackBlueGreen(state, phase, logPath, fmt.Sprintf("stop blue daemon: %v", err))
	}
	if err := promoteGreenToLive(state, app, phase); err != nil {
		// Blue is already stopped: never leave a silent zero-daemon window.
		// The staged green passed health gates seconds ago — recover by
		// promoting it live through the same cutover machinery.
		if emberr := emergencyPromoteLiveGreen(state, app, phase); emberr != nil {
			return fmt.Errorf("cutover failed after blue stopped (green standby pid %d): %v; emergency live-promote also failed: %v", greenPID, err, emberr)
		}
		fmt.Printf("[ai] cutover recovered via emergency live-promote green=%d\n", greenPID)
	}
	stagedOK = true
	return confirmGreenLive(state, logPath, greenPID, oldVersion, newVersion)
}

// runDaemonStandby boots the staged binary in probation mode: no Discord
// intake gateway connect (deferred until cutover), shadow pid/heartbeat
// paths, handoff consumption, and the green-ready log marker. The standby
// touches its shadow heartbeat while watching the phase file; once the
// updater marks cutover-done it rewrites the live pid, seeds the live
// heartbeat, logs live enablement, and serves live via run.
func runDaemonStandby() error {
	state, err := stateRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(state, 0o755); err != nil {
		return err
	}
	if err := os.Chdir(state); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	me := os.Getpid()
	_ = os.WriteFile(greenPIDPathForRoot(state), []byte(strconv.Itoa(me)+"\n"), 0o644)
	consumeUpdateHandoff(state)
	touchGreenHeartbeat(state)
	log.Printf("blue-green green-ready pid=%d standby probation", me)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	deadline := time.Now().Add(blueGreenPhaseExpiry)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-tick.C:
			touchGreenHeartbeat(state)
			if now.After(deadline) {
				// Probation over with no cutover: leave no orphans behind.
				// A stale phase file would otherwise linger (and mislead a
				// future handover), as would a consumed-but-unread handoff.
				_ = os.Remove(greenPIDPathForRoot(state))
				_ = os.Remove(greenHeartbeatPathForRoot(state))
				clearBlueGreenPhase(state)
				clearUpdateHandoff(state)
				return fmt.Errorf("blue-green standby expired without cutover")
			}
			phase, err := readBlueGreenPhase(state)
			if err != nil {
				continue
			}
			if !phase.CutoverDone {
				continue
			}
			_ = os.WriteFile(filepath.Join(state, "ai.pid"), []byte(strconv.Itoa(me)+"\n"), 0o644)
			touchLiveHeartbeat(state)
			log.Printf("blue-green live intake enabled pid=%d", me)
			return run(ctx, false)
		}
	}
}

func touchGreenHeartbeat(state string) {
	if strings.TrimSpace(state) == "" {
		return
	}
	_ = os.WriteFile(greenHeartbeatPathForRoot(state), []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
}

func touchLiveHeartbeat(state string) {
	if strings.TrimSpace(state) == "" {
		return
	}
	_ = os.WriteFile(liveHeartbeatPathForRoot(state), []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
}
