package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func runUpdate() error {
	root, err := installRoot()
	if err != nil { return err }

	before, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil { return err }
	if err := runCommand(root, "git", "pull", "--ff-only"); err != nil { return err }
	after, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil { return err }
	if strings.TrimSpace(before) == strings.TrimSpace(after) { return nil }

	app := filepath.Join(root, "ai")
	tmp := app + ".update"
	goBin := filepath.Join(root, ".toolchain", "go", "bin", "go")
	if _, err := os.Stat(goBin); err != nil { goBin = "go" }
	if err := runCommand(root, goBin, "build", "-trimpath", "-ldflags", "-s -w", "-o", tmp, "./cmd/ai"); err != nil { _ = os.Remove(tmp); return err }
	if err := os.Chmod(tmp, 0o755); err != nil { _ = os.Remove(tmp); return err }
	if err := os.Rename(tmp, app); err != nil { _ = os.Remove(tmp); return err }

	supervisor := filepath.Join(root, "scripts", "supervisor.sh")
	if _, err := os.Stat(supervisor); err != nil { return fmt.Errorf("supervisor: %w", err) }
	if err := runCommand(root, "bash", supervisor, "stop"); err != nil { return err }

	cmd := exec.Command("bash", supervisor, "run")
	cmd.Dir = root
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil { return err }
	return cmd.Process.Release()
}

func installRoot() (string, error) {
	if cwd, err := os.Getwd(); err == nil {
		if _, statErr := os.Stat(filepath.Join(cwd, ".git")); statErr == nil {
			return filepath.Abs(cwd)
		}
	}
	exe, err := os.Executable()
	if err != nil { return "", err }
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil { return "", err }
	root := filepath.Dir(exe)
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return "", fmt.Errorf("ai installation root not found from %s: %w", exe, err)
	}
	return root, nil
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
