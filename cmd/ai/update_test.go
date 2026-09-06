package main

import "testing"

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
