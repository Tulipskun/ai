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

func TestRestartDaemonAfterUpdate(t *testing.T) {
	if err := restartDaemonAfterUpdate("/definitely/not/an/ai-binary", false); err != nil {
		t.Fatalf("stopped daemon should not be restarted: %v", err)
	}
	if err := restartDaemonAfterUpdate("/definitely/not/an/ai-binary", true); err == nil {
		t.Fatal("running daemon should attempt a restart")
	}
}
