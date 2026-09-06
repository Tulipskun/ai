package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func runUpdate() error {
	app, err := installedBinary()
	if err != nil { return err }

	repo := envOr("AI_REPO", "Tulipskun/ai")
	version := envOr("AI_VERSION", "latest")
	assetOS := runtime.GOOS
	assetArch := runtime.GOARCH
	if assetOS != "linux" && assetOS != "darwin" { return fmt.Errorf("unsupported operating system: %s", assetOS) }
	if assetArch != "amd64" && assetArch != "arm64" { return fmt.Errorf("unsupported architecture: %s", assetArch) }
	asset := fmt.Sprintf("ai-%s-%s", assetOS, assetArch)

	base := fmt.Sprintf("https://github.com/%s/releases/latest/download", repo)
	if version != "latest" { base = fmt.Sprintf("https://github.com/%s/releases/download/%s", repo, version) }

	binary, err := download(fmt.Sprintf("%s/%s", base, asset))
	if err != nil { return fmt.Errorf("download %s: %w", asset, err) }
	checksums, err := download(fmt.Sprintf("%s/checksums.txt", base))
	if err != nil { return fmt.Errorf("download checksums: %w", err) }
	expected, err := checksumForAsset(checksums, asset)
	if err != nil { return err }
	actual := fmt.Sprintf("%x", sha256.Sum256(binary))
	if actual != expected { return fmt.Errorf("checksum verification failed for %s", asset) }

	tmp, err := os.CreateTemp(filepath.Dir(app), ".ai-update-*")
	if err != nil { return err }
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(binary); err != nil { _ = tmp.Close(); return err }
	if err := tmp.Chmod(0o755); err != nil { _ = tmp.Close(); return err }
	if err := tmp.Close(); err != nil { return err }
	if err := os.Rename(tmpPath, app); err != nil { return fmt.Errorf("replace binary: %w", err) }

	fmt.Printf("[ai] updated %s\n", app)
	if supervisor := filepath.Join(filepath.Dir(app), "scripts", "supervisor.sh"); fileExists(supervisor) {
		fmt.Println("[ai] legacy supervisor detected; use it to restart the running process")
	}
	return nil
}

func installedBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil { return "", err }
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil { return "", err }
	if !fileExists(exe) { return "", fmt.Errorf("installed binary not found: %s", exe) }
	return exe, nil
}

func download(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { return nil, fmt.Errorf("HTTP %d", resp.StatusCode) }
	return io.ReadAll(resp.Body)
}

func checksumForAsset(data []byte, asset string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == asset { return fields[0], nil }
	}
	return "", fmt.Errorf("checksum entry not found for %s", asset)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
