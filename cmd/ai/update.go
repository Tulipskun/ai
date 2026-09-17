package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

func runUpdate(args []string) error {
	targetVersion, _, err := parseUpdateArgs(args)
	if err != nil {
		return err
	}
	app, err := installedBinary()
	if err != nil {
		return err
	}
	const repo = "Tulipskun/ai"
	assetOS := runtime.GOOS
	assetArch := runtime.GOARCH
	if assetOS != "linux" {
		return fmt.Errorf("unsupported operating system: %s (only linux is supported)", assetOS)
	}
	if assetArch != "arm64" {
		return fmt.Errorf("unsupported architecture: %s (only arm64 is supported)", assetArch)
	}
	asset := fmt.Sprintf("ai-%s-%s", assetOS, assetArch)
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh CLI is required for ai update (install from https://cli.github.com and run: gh auth login): %w", err)
	}
	tmpDir, err := os.MkdirTemp("", "ai-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	if err := ghReleaseDownload(repo, targetVersion, tmpDir, []string{asset, "checksums.txt"}); err != nil {
		return err
	}
	binary, err := os.ReadFile(filepath.Join(tmpDir, asset))
	if err != nil {
		return fmt.Errorf("read downloaded %s: %w", asset, err)
	}
	checksums, err := os.ReadFile(filepath.Join(tmpDir, "checksums.txt"))
	if err != nil {
		return fmt.Errorf("read downloaded checksums: %w", err)
	}
	expected, err := checksumForAsset(checksums, asset)
	if err != nil {
		return err
	}
	actual := fmt.Sprintf("%x", sha256.Sum256(binary))
	if actual != expected {
		return fmt.Errorf("checksum verification failed for %s", asset)
	}
	newVersion, err := ghReleaseTag(repo, targetVersion)
	if err != nil {
		return err
	}
	oldVersion := strings.TrimSpace(version)
	if oldVersion == "" || oldVersion == "dev" {
		oldVersion = "unknown"
	}
	oldBinary, err := os.ReadFile(app)
	if err != nil {
		return fmt.Errorf("read installed %s: %w", asset, err)
	}
	oldHash := fmt.Sprintf("%x", sha256.Sum256(oldBinary))
	newHash := fmt.Sprintf("%x", sha256.Sum256(binary))
	fmt.Printf("[ai] version: %s -> %s\n", oldVersion, newVersion)
	if oldHash == newHash {
		fmt.Println("[ai] binary unchanged; no restart")
		return nil
	}
	fmt.Println("[ai] binary changed; updating")
	tmp, err := os.CreateTemp(filepath.Dir(app), ".ai-update-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
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
	// Graceful handoff (REQ-043): drain in-flight work before replacing the
	// binary. waitForJobsDrain lets background jobs finish; the daemon keeps
	// serving until SIGTERM so the current turn can settle and session DBs
	// stay persisted. Intake needs no queue file: Discord/CLI inputs resume
	// from the live transports on the new daemon.
	state, _ := stateRoot()
	oldPID := 0
	if state != "" {
		oldPID = currentDaemonPID(state)
	}
	wasRunning := daemonRunning()
	drained := true
	if wasRunning {
		if jobsPath := jobsFilePathForUpdate(state); jobsPath != "" {
			drained = waitForJobsDrain(jobsPath, updateJobsDrainTimeout)
		}
		if _, err := stopDaemonForUpdate(updateDrainTimeout); err != nil {
			return fmt.Errorf("stop daemon for update: %w", err)
		}
	}
	if err := os.Rename(tmpPath, app); err != nil {
		if wasRunning {
			if restartErr := startDaemon(app); restartErr != nil {
				return fmt.Errorf("replace binary: %w; restore daemon: %v", err, restartErr)
			}
		}
		return fmt.Errorf("replace binary: %w", err)
	}
	fmt.Printf("[ai] updated %s\n", app)
	if state != "" {
		_ = writeUpdateHandoff(state, updateHandoff{
			OldVersion: oldVersion,
			NewVersion: newVersion,
			OldHash:    oldHash,
			NewHash:    newHash,
			PID:        oldPID,
			Drained:    drained,
		})
	}
	if err := restartDaemonAfterUpdate(app, wasRunning); err != nil {
		return fmt.Errorf("restart daemon: %w", err)
	}
	if wasRunning {
		if err := verifyDaemonHealthy(updateHealthTimeout); err != nil {
			return err
		}
		fmt.Println("[ai] new daemon healthy")
	}
	return nil
}

func jobsFilePathForUpdate(state string) string {
	if strings.TrimSpace(state) == "" {
		return ""
	}
	return filepath.Join(state, "data", "jobs.json")
}

// ghReleaseDownload downloads release assets with `gh release download`.
// Empty version or "latest" means the GitHub latest release, otherwise an explicit tag.
func ghReleaseTag(repo, version string) (string, error) {
	args := []string{"release", "view"}
	if !isLatestVersion(version) {
		args = append(args, strings.TrimSpace(version))
	}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	args = append(args, "--json", "tagName", "--jq", ".tagName")
	cmd := exec.Command("gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("gh release view %s: %s", releaseLabel(version), detail)
	}
	tag := strings.TrimSpace(stdout.String())
	if tag == "" {
		return "", fmt.Errorf("release tag is empty")
	}
	return tag, nil
}

func ghReleaseDownload(repo, version, dir string, patterns []string) error {
	args := ghReleaseDownloadArgs(repo, version, dir, patterns)
	cmd := exec.Command("gh", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("gh release download %s: %s", releaseLabel(version), detail)
	}
	return nil
}

func ghReleaseDownloadArgs(repo, version, dir string, patterns []string) []string {
	args := []string{"release", "download"}
	if !isLatestVersion(version) {
		args = append(args, strings.TrimSpace(version))
	}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	for _, p := range patterns {
		if strings.TrimSpace(p) == "" {
			continue
		}
		args = append(args, "--pattern", p)
	}
	args = append(args, "--dir", dir, "--clobber")
	return args
}

func isLatestVersion(version string) bool {
	v := strings.TrimSpace(version)
	return v == "" || strings.EqualFold(v, "latest")
}

func releaseLabel(version string) string {
	if isLatestVersion(version) {
		return "latest"
	}
	return strings.TrimSpace(version)
}

func restartDaemonAfterUpdate(app string, wasRunning bool) error {
	if !wasRunning {
		return nil
	}
	return startDaemon(app)
}
func parsePID(value string) int {
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &pid); err != nil {
		return 0
	}
	return pid
}
func installedBinary() (string, error) {
	argv0 := strings.TrimSpace(os.Args[0])
	if argv0 == "" {
		return "", fmt.Errorf("cannot determine invoked binary path")
	}
	var exe string
	if strings.ContainsRune(argv0, os.PathSeparator) {
		exe = argv0
		if !filepath.IsAbs(exe) {
			var err error
			exe, err = filepath.Abs(exe)
			if err != nil {
				return "", err
			}
		}
	} else {
		var err error
		exe, err = exec.LookPath(argv0)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(exe) {
			exe, err = filepath.Abs(exe)
			if err != nil {
				return "", err
			}
		}
	}
	if !fileExists(exe) {
		return "", fmt.Errorf("installed binary not found: %s", exe)
	}
	return filepath.Clean(exe), nil
}
func stateRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		if current, userErr := user.Current(); userErr == nil && current.HomeDir != "" {
			return filepath.Join(current.HomeDir, ".local", "share", "ai"), nil
		}
		return "", err
	}
	return filepath.Join(home, ".local", "share", "ai"), nil
}
func daemonRunning() bool {
	root, err := stateRoot()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(root, "ai.pid"))
	if err != nil {
		return false
	}
	return processAlive(parsePID(string(data)))
}
func checksumForAsset(data []byte, asset string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == asset {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksum entry not found for %s", asset)
}
func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }
