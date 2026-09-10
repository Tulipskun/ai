package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLineEditorHistoryPersistsWithoutProviderSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history")
	f, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	editor := NewLineEditor(f, os.Stdout)
	editor.HistoryPath = path
	if err := editor.AppendHistory("hello world"); err != nil {
		t.Fatal(err)
	}
	if err := editor.AppendHistory("/provider add openai openai https://example.invalid sk-secret"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "hello world") {
		t.Fatalf("history missing normal command: %q", got)
	}
	if strings.Contains(got, "sk-secret") {
		t.Fatalf("provider secret was persisted: %q", got)
	}
}
