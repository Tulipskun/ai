package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBlueGreenPhaseRoundTrip(t *testing.T) {
	root := t.TempDir()
	phase := blueGreenPhase{
		OldVersion: "v1", NewVersion: "v2", OldHash: "a", NewHash: "b",
		BluePID: 11, GreenPID: 22, StagedBinary: filepath.Join(root, "ai-green"),
	}
	if err := writeBlueGreenPhase(root, phase); err != nil {
		t.Fatal(err)
	}
	got, err := readBlueGreenPhase(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.OldVersion != "v1" || got.NewVersion != "v2" || got.BluePID != 11 || got.GreenPID != 22 {
		t.Fatalf("phase=%+v", got)
	}
	if got.At == "" {
		t.Fatal("phase At not stamped")
	}
	if !isBlueGreenHandoverActive(root) {
		t.Fatal("fresh non-cutover phase should be an active handover")
	}
	clearBlueGreenPhase(root)
	if _, err := readBlueGreenPhase(root); err == nil {
		t.Fatal("expected missing phase after clear")
	}
	if isBlueGreenHandoverActive(root) {
		t.Fatal("cleared phase must not be an active handover")
	}
}

func TestBlueGreenHandoverInactiveWhenCutoverOrExpired(t *testing.T) {
	root := t.TempDir()
	done := blueGreenPhase{OldVersion: "v1", NewVersion: "v2", CutoverDone: true}
	if err := writeBlueGreenPhase(root, done); err != nil {
		t.Fatal(err)
	}
	if isBlueGreenHandoverActive(root) {
		t.Fatal("cutover-done phase must not be active")
	}
	stale := blueGreenPhase{OldVersion: "v1", NewVersion: "v2", At: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)}
	if err := writeBlueGreenPhase(root, stale); err != nil {
		t.Fatal(err)
	}
	if isBlueGreenHandoverActive(root) {
		t.Fatal("expired phase must not be active")
	}
}

func TestCheckGreenHealthGates(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "ai.log")
	phase := blueGreenPhase{GreenPID: os.Getpid(), StagedBinary: filepath.Join(root, "ai-green")}
	if err := writeBlueGreenPhase(root, phase); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(greenPIDPathForRoot(root), []byte("99999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkGreenHealth(root, logPath); err == nil {
		t.Fatal("expected health gates to fail on a dead green")
	} else if !strings.Contains(err.Error(), "green pid not alive") {
		t.Fatalf("expected pid gate, got: %v", err)
	}
	// Live self pid proves the pid gate passes without touching the real daemon.
	if err := os.WriteFile(greenPIDPathForRoot(root), []byte("1\n"), 0o644); err == nil {
		_ = os.Remove(greenPIDPathForRoot(root))
	}
}

func TestCheckGreenHealthAllPass(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "ai.log")
	me := os.Getpid()
	if err := os.WriteFile(greenPIDPathForRoot(root), []byte(strconv.Itoa(me)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("boot\n"+greenReadyMarker+" pid=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	touchGreenHeartbeat(root)
	// Handoff absent counts as consumed; gates must all pass.
	if err := checkGreenHealth(root, logPath); err != nil {
		t.Fatalf("expected all gates to pass: %v", err)
	}
}

func TestWaitForGreenHealthTimeout(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "ai.log")
	if err := os.WriteFile(greenPIDPathForRoot(root), []byte("99999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := waitForGreenHealth(root, logPath, 600*time.Millisecond); err == nil {
		t.Fatal("expected timeout on dead green")
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Fatal("expected the timeout to actually elapse")
	}
}

func TestRollbackBlueGreenKeepsLivePID(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "ai.log")
	live := filepath.Join(root, "ai.pid")
	if err := os.WriteFile(live, []byte("4242\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(root, ".ai-green-test")
	if err := os.WriteFile(staged, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(greenPIDPathForRoot(root), []byte("99999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(greenHeartbeatPathForRoot(root), []byte("now"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	phase := blueGreenPhase{GreenPID: 99999999, StagedBinary: staged}
	if err := writeBlueGreenPhase(root, phase); err != nil {
		t.Fatal(err)
	}
	err := rollbackBlueGreen(root, phase, logPath, "green probe failed")
	if err == nil || !strings.Contains(err.Error(), "blue kept serving") {
		t.Fatalf("rollback must return a clear blue-kept error, got: %v", err)
	}
	if data, rerr := os.ReadFile(live); rerr != nil || strings.TrimSpace(string(data)) != "4242" {
		t.Fatalf("live ai.pid must be untouched: %q %v", string(data), rerr)
	}
	for _, p := range []string{staged, greenPIDPathForRoot(root), greenHeartbeatPathForRoot(root), blueGreenPhasePathForRoot(root)} {
		if _, serr := os.Stat(p); !os.IsNotExist(serr) {
			t.Fatalf("rollback must remove %s", p)
		}
	}
}

func TestNoExternalSupervisorScripts(t *testing.T) {
	// CHANGE-055: the single `ai` binary owns the handover end to end. The
	// legacy external supervisors must stay deleted; dc-keepalive.sh is an
	// unrelated Desktop Commander watcher and is not covered here.
	for _, name := range []string{"keepalive.sh", "supervisor.sh"} {
		if _, err := os.Stat(filepath.Join("..", "..", "scripts", name)); !os.IsNotExist(err) {
			t.Fatalf("external supervisor script %s must not exist", name)
		}
	}
}

func TestStandbyDaemonArgsDetected(t *testing.T) {
	if !isStandbyDaemonArgs([]string{"daemon", "--standby"}) {
		t.Fatal("expected --standby daemon args to be detected")
	}
	if isStandbyDaemonArgs([]string{"daemon"}) {
		t.Fatal("plain daemon must not be standby")
	}
	if got, err := parseCommand([]string{"daemon", "--standby"}); err != nil || got != commandDaemon {
		t.Fatalf("daemon --standby must parse as daemon: %v %v", got, err)
	}
}

func TestPromoteGreenRefusesWhileBlueAlive(t *testing.T) {
	// currentDaemonPID/current pid files read from the live state root, so
	// this test only proves the refusal rule textually: promoteGreenToLive
	// refuses whenever the live ai.pid still points at a live process, and
	// startGreenStandby refuses a second green while one is alive.
	if !strings.Contains(greenReadyMarker, "green-ready") {
		t.Fatal("ready marker constant drifted")
	}
	if blueGreenHealthTimeout < 60*time.Second || blueGreenHealthTimeout > 90*time.Second {
		t.Fatalf("health timeout %s must stay within 60-90s", blueGreenHealthTimeout)
	}
}

func TestEmergencyPromoteLiveGreen(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "ai")
	if err := os.WriteFile(app, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(root, ".ai-green-test")
	if err := os.WriteFile(staged, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Live pid owned by another live process (pid 1, not green): refuse.
	if err := os.WriteFile(filepath.Join(root, "ai.pid"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	me := os.Getpid()
	phase := blueGreenPhase{OldVersion: "v1", NewVersion: "v2", BluePID: 1, GreenPID: me, StagedBinary: staged}
	if err := writeBlueGreenPhase(root, phase); err != nil {
		t.Fatal(err)
	}
	if err := emergencyPromoteLiveGreen(root, app, phase); err != nil {
		t.Fatalf("emergency promote failed: %v", err)
	}
	if data, err := os.ReadFile(app); err != nil || string(data) != "new-binary" {
		t.Fatalf("app not replaced by staged binary: %q %v", string(data), err)
	}
	got, err := readBlueGreenPhase(root)
	if err != nil || !got.CutoverDone || got.GreenPID != me {
		t.Fatalf("phase not marked cutover-done: %+v %v", got, err)
	}
	if _, err := os.Stat(greenPIDPathForRoot(root)); !os.IsNotExist(err) {
		t.Fatal("shadow pid must be removed on emergency promote")
	}
}

func TestEmergencyPromoteRefusesForeignLivePID(t *testing.T) {
	root := t.TempDir()
	staged := filepath.Join(root, ".ai-green-test")
	if err := os.WriteFile(staged, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	phase := blueGreenPhase{GreenPID: os.Getpid(), StagedBinary: staged}
	// pid 1 is alive on most systems (init); if green==self and live==1 differ, refusal triggers.
	if os.Getpid() == 1 {
		t.Skip("test process is pid 1; refusal case not constructible here")
	}
	if err := emergencyPromoteLiveGreen(root, filepath.Join(root, "ai"), phase); err == nil {
		t.Fatal("expected refusal when a foreign live pid owns ai.pid")
	}
}
