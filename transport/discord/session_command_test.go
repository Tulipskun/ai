package discord

import (
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func TestSessionCommandRequiresSelector(t *testing.T) {
	called := false
	h := &SessionCommandHandler{SelectSession: func(channelID, sessionID string) error {
		called = channelID == "channel-1" && sessionID == "session-1"
		return nil
	}}
	if h.SelectSession == nil { t.Fatal("selector not configured") }
	if err := h.SelectSession("channel-1", "session-1"); err != nil { t.Fatal(err) }
	if !called { t.Fatal("session selector did not receive channel/session IDs") }
}

func TestSessionSelectionOptionsCarrySessionIDs(t *testing.T) {
	items := []sdk.SessionInfo{{ID: "discord:channel:a", Provider: "openrouter", Model: "m1"}, {ID: "discord:channel:b", Provider: "openai", Model: "m2"}}
	seen := map[string]bool{}
	for _, item := range items { seen[item.ID] = true }
	if !seen["discord:channel:a"] || !seen["discord:channel:b"] { t.Fatal("session IDs missing") }
}
