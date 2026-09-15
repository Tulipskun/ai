package discord

import (
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
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

func sessionListInteraction() *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      discordgo.InteractionApplicationCommand,
		ChannelID: "chan-1",
		Data:      discordgo.ApplicationCommandInteractionData{Name: "session"},
	}}
}

func TestSessionListDefersThenFollowsUpWithMenu(t *testing.T) {
	handler := &SessionCommandHandler{ListSessions: func(int) ([]sdk.SessionInfo, error) {
		return []sdk.SessionInfo{{ID: "discord:channel:a", Provider: "openrouter", Model: "m1"}}, nil
	}}
	fake := &fakeInteractionAPI{}
	if err := handler.Handle(fake, sessionListInteraction()); err != nil {
		t.Fatal(err)
	}
	if len(fake.responds) != 1 || fake.responds[0].Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("list must defer first: %+v", fake.responds)
	}
	if len(fake.followups) != 1 {
		t.Fatalf("list outcome must be a followup: %+v", fake.followups)
	}
	followup := fake.followups[0]
	if followup.Flags != discordgo.MessageFlagsEphemeral {
		t.Fatalf("followup must be ephemeral: %+v", followup)
	}
	found := false
	for _, component := range followup.Components {
		row, ok := component.(discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, child := range row.Components {
			menu, ok := child.(discordgo.SelectMenu)
			if !ok || menu.CustomID != "session:select" {
				continue
			}
			for _, option := range menu.Options {
				if option.Value == "discord:channel:a" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatalf("followup menu missing session option: %+v", followup.Components)
	}
}

func TestSessionListFailureFollowsUpError(t *testing.T) {
	handler := &SessionCommandHandler{ListSessions: func(int) ([]sdk.SessionInfo, error) {
		return nil, errors.New("store unavailable")
	}}
	fake := &fakeInteractionAPI{}
	if err := handler.Handle(fake, sessionListInteraction()); err != nil {
		t.Fatal(err)
	}
	if len(fake.responds) != 1 || len(fake.followups) != 1 || fake.followups[0].Content != "store unavailable" {
		t.Fatalf("list failure must defer then follow up the error: %+v %+v", fake.responds, fake.followups)
	}
}
