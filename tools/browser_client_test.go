package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBrowserClientDefaultsToChromiumPipe(t *testing.T) {
	client := NewBrowserClient(BrowserClientConfig{})
	if client.cfg.Browser != "chromium" { t.Fatalf("browser = %q", client.cfg.Browser) }
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

func TestBrowserAutoPrefersFirefox(t *testing.T) {
	got := browserCandidates("auto")
	if len(got) < 2 || got[0] != "firefox" || got[1] != "firefox-esr" {
		t.Fatalf("auto candidates = %#v, want firefox first", got)
	}
}

func TestBrowserPrepareLazyClient(t *testing.T) {
	client := NewBrowserClient(BrowserClientConfig{Browser: "firefox", Profile: t.TempDir(), Lazy: true})
	if !client.cfg.Lazy || client.Ready() {
		t.Fatalf("lazy client should start unready: %#v", client.cfg)
	}
}

func TestBrowserDisplayEnv(t *testing.T) {
	if got := browserDisplayEnv(""); got != nil {
		t.Fatalf("empty display should inherit env, got %v", got)
	}
	t.Setenv("DISPLAY", ":0")
	got := browserDisplayEnv(":1")
	found := false
	for _, kv := range got {
		if kv == "DISPLAY=:1" {
			found = true
		}
		if kv == "DISPLAY=:0" {
			t.Fatalf("old DISPLAY leaked through: %v", got)
		}
	}
	if !found {
		t.Fatalf("DISPLAY=:1 missing: %v", got)
	}
}
