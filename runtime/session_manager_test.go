package runtime

import (
	"context"
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func TestSessionManagerReusesSessionByID(t *testing.T) {
	manager := NewSessionManager(t.TempDir()+"/sessions.db", sdk.SessionConfig{Provider: sdk.ProviderOpenRouter, Model: "model"}, sdk.NewKeyPool("key"))
	first, err := manager.Resolve(context.Background(), sdk.Input{SessionID: "discord:channel:1"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Resolve(context.Background(), sdk.Input{SessionID: "discord:channel:1"})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("same SessionID created multiple session objects")
	}
}

func TestSessionManagerUsesInputSessionID(t *testing.T) {
	manager := NewSessionManager(t.TempDir()+"/sessions.db", sdk.SessionConfig{Provider: sdk.ProviderOpenRouter, Model: "model"}, sdk.NewKeyPool("key"))
	session, err := manager.Resolve(context.Background(), sdk.Input{SessionID: "telegram:chat:42"})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID() != "telegram:chat:42" {
		t.Fatalf("session ID = %q", session.ID())
	}
}

func TestSessionManagerAdoptsProvidersAndRepointsStaleSessions(t *testing.T) {
	manager := NewSessionManager(t.TempDir()+"/sessions.db", sdk.SessionConfig{}, nil)
	stale, err := manager.Resolve(context.Background(), sdk.Input{SessionID: "work-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := stale.Config().Provider; got != "" {
		t.Fatalf("session provider before adopt = %q, want empty", got)
	}

	manager.AdoptProviders(
		[]sdk.ProviderConfig{{ID: "NousResearch", Keys: sdk.NewKeyPool("key")}},
		sdk.SessionConfig{Provider: "NousResearch", Model: "meituan/longcat-2.0:free"},
	)

	if got := stale.Config().Provider; got != "NousResearch" {
		t.Fatalf("cached session provider = %q, want NousResearch", got)
	}
	if got := stale.Config().Model; got != "meituan/longcat-2.0:free" {
		t.Fatalf("cached session model = %q, want the reloaded model", got)
	}
	reopened, err := manager.Resolve(context.Background(), sdk.Input{SessionID: "work-2"})
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Config().Provider; got != "NousResearch" {
		t.Fatalf("session opened after adopt = %q, want NousResearch", got)
	}
}
