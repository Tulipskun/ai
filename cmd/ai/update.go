package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func runUpdate() error {
	root, err := os.Getwd()
	if err != nil { return err }
	root, err = filepath.Abs(root)
	if err != nil { return err }

	before, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil { return err }
	if err := runCommand(root, "git", "pull", "--ff-only"); err != nil { return err }
	after, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil { return err }
	if strings.TrimSpace(before) == strings.TrimSpace(after) { return nil }

	app := filepath.Join(root, "ai")
	tmp := app + ".update"
	if err := runCommand(root, "go", "build", "-o", tmp, "./cmd/ai"); err != nil { _ = os.Remove(tmp); return err }
	if err := os.Chmod(tmp, 0o755); err != nil { _ = os.Remove(tmp); return err }
	if err := os.Rename(tmp, app); err != nil { _ = os.Remove(tmp); return err }

	supervisor := filepath.Join(root, "scripts", "supervisor.sh")
	if _, err := os.Stat(supervisor); err != nil { return fmt.Errorf("supervisor: %w", err) }

	stateDir := filepath.Join(root, ".ai")
	supervisorPIDFile := filepath.Join(stateDir, "supervisor.pid")
	aiPIDFile := filepath.Join(stateDir, "ai.pid")

	stopPIDFile(aiPIDFile)
	stopPIDFile(supervisorPIDFile)

	cmd := exec.Command("bash", supervisor, "run")
	cmd.Dir = root
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil { return err }
	_ = cmd.Process.Release()
	return nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil { return "", fmt.Errorf("%s: %w", strings.Join(args, " "), err) }
	return string(out), nil
}

func runCommand(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func stopPIDFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil { return }
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 || pid == os.Getpid() { return }
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil { break }
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	_ = os.Remove(path)
}
