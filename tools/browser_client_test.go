package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBrowserClientDefaultsToInstalledBrowser(t *testing.T) {
	client := NewBrowserClient(BrowserClientConfig{})
	if client.cfg.Browser != "auto" { t.Fatalf("browser = %q", client.cfg.Browser) }
	if client.cfg.IdleTimeout != 30*time.Minute || client.cfg.NavigationTimeout != 30*time.Second || client.cfg.ActionTimeout != 10*time.Second || client.cfg.SnapshotTimeout != 10*time.Second {
		t.Fatalf("unexpected defaults: %#v", client.cfg)
	}
	if client.cfg.Headless { t.Fatal("browser must default to headed mode") }
}

func TestBrowserCandidates(t *testing.T) {
	cases := map[string][]string{
		"chrome": {"google-chrome", "google-chrome-stable", "chrome"},
		"chromium": {"chromium", "chromium-browser"},
		"edge": {"microsoft-edge", "microsoft-edge-stable", "msedge"},
	}
	for preference, want := range cases {
		got := browserCandidates(preference)
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") { t.Fatalf("%s candidates = %#v, want %#v", preference, got, want) }
	}
}

func TestBrowserClientCallWithoutStart(t *testing.T) {
	client := NewBrowserClient(BrowserClientConfig{})
	if err := client.Call(context.Background(), "browser.open", map[string]string{"session_id":"test"}, nil); err == nil || !strings.Contains(err.Error(), "browser is unavailable") {
		t.Fatalf("error = %v", err)
	}
}
