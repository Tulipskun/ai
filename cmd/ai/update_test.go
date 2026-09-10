package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChecksumForAsset(t *testing.T) {
	data := []byte("abc123  ai-linux-arm64\n")
	got, err := checksumForAsset(data, "ai-linux-arm64")
	if err != nil { t.Fatal(err) }
	if got != "abc123" { t.Fatalf("checksum = %q", got) }
}

func TestChecksumForAssetMissing(t *testing.T) {
	if _, err := checksumForAsset([]byte("abc123  ai-linux-arm64\n"), "ai-linux-amd64"); err == nil {
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

func TestIsLatestVersion(t *testing.T) {
	for _, v := range []string{"", "latest", "Latest", "LATEST", "  latest  "} {
		if !isLatestVersion(v) {
			t.Fatalf("expected latest for %q", v)
		}
	}
	for _, v := range []string{"v1.0.0", "main", "stable"} {
		if isLatestVersion(v) {
			t.Fatalf("expected explicit tag for %q", v)
		}
	}
}

func TestGhReleaseDownloadArgsLatest(t *testing.T) {
	args := ghReleaseDownloadArgs("Tulipskun/ai", "latest", "/tmp/dl", []string{"ai-linux-arm64", "checksums.txt"})
	joined := " " + strings.Join(args, " ") + " "
	for _, want := range []string{" release download ", "--repo Tulipskun/ai", "--pattern ai-linux-arm64", "--pattern checksums.txt", "--dir /tmp/dl", "--clobber"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, " latest ") {
		t.Fatalf("latest release should not pass an explicit tag: %q", joined)
	}
}

func TestGhReleaseDownloadArgsPinnedTag(t *testing.T) {
	args := ghReleaseDownloadArgs("Tulipskun/ai", "v1.2.3", "/tmp/dl", []string{"ai-linux-arm64", "checksums.txt"})
	joined := " " + strings.Join(args, " ") + " "
	if !strings.Contains(joined, " download v1.2.3 ") {
		t.Fatalf("pinned tag should be passed explicitly: %q", joined)
	}
}
