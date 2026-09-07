package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChecksumForAsset(t *testing.T) {
	data := []byte("abc123  ai-linux-amd64\ndef456  ai-darwin-arm64\n")
	got, err := checksumForAsset(data, "ai-linux-amd64")
	if err != nil { t.Fatal(err) }
	if got != "abc123" { t.Fatalf("checksum = %q", got) }
}

func TestChecksumForAssetMissing(t *testing.T) {
	if _, err := checksumForAsset([]byte("abc123  ai-linux-amd64\n"), "ai-linux-arm64"); err == nil {
		t.Fatal("expected missing checksum error")
	}
}

func TestInstalledBinaryPreservesSymlinkInvocationPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "legacy", "ai")
	invoked := filepath.Join(root, "bin", "ai")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { t.Fatal(err) }
	if err := os.MkdirAll(filepath.Dir(invoked), 0o755); err != nil { t.Fatal(err) }
	if err := os.WriteFile(target, []byte("binary"), 0o755); err != nil { t.Fatal(err) }
	if err := os.Symlink(target, invoked); err != nil { t.Fatal(err) }

	old := os.Args[0]
	defer func() { os.Args[0] = old }()
	os.Args[0] = invoked

	got, err := installedBinary()
	if err != nil { t.Fatal(err) }
	if got != invoked { t.Fatalf("installed binary = %q, want %q", got, invoked) }
}

func TestStatePathResolvesRelativeValuesAgainstStateRoot(t *testing.T) {
	state := filepath.Join(t.TempDir(), ".local", "share", "ai")
	old := os.Getenv("AI_PROVIDER_CONFIG")
	defer func() { _ = os.Setenv("AI_PROVIDER_CONFIG", old) }()
	_ = os.Setenv("AI_PROVIDER_CONFIG", "custom/provider.json")

	got := statePath(state, "AI_PROVIDER_CONFIG", ".config/provider.json")
	want := filepath.Join(state, "custom/provider.json")
	if got != want { t.Fatalf("state path = %q, want %q", got, want) }
}

func TestStatePathPreservesAbsoluteValues(t *testing.T) {
	state := filepath.Join(t.TempDir(), ".local", "share", "ai")
	absolute := filepath.Join(t.TempDir(), "provider.json")
	old := os.Getenv("AI_PROVIDER_CONFIG")
	defer func() { _ = os.Setenv("AI_PROVIDER_CONFIG", old) }()
	_ = os.Setenv("AI_PROVIDER_CONFIG", absolute)

	got := statePath(state, "AI_PROVIDER_CONFIG", ".config/provider.json")
	if got != absolute { t.Fatalf("state path = %q, want %q", got, absolute) }
}
