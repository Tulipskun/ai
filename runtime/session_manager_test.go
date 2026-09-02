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
