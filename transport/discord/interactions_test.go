package discord

import (
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// fakeInteractionAPI records deferred replies without any network.
type fakeInteractionAPI struct {
	responds    []*discordgo.InteractionResponse
	followups   []*discordgo.WebhookParams
	respondErr  error
	followupErr error
}

func (f *fakeInteractionAPI) InteractionRespond(_ *discordgo.Interaction, resp *discordgo.InteractionResponse, _ ...discordgo.RequestOption) error {
	f.responds = append(f.responds, resp)
	return f.respondErr
}

func (f *fakeInteractionAPI) FollowupMessageCreate(_ *discordgo.Interaction, _ bool, data *discordgo.WebhookParams, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	f.followups = append(f.followups, data)
	if f.followupErr != nil {
		return nil, f.followupErr
	}
	return &discordgo.Message{ID: "msg-1"}, nil
}

func testInteraction() *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      discordgo.InteractionApplicationCommand,
		GuildID:   "guild-1",
		ChannelID: "chan-1",
	}}
}

func TestDeferEphemeralResponse(t *testing.T) {
	fake := &fakeInteractionAPI{}
	if err := deferEphemeralResponse(fake, testInteraction()); err != nil {
		t.Fatal(err)
	}
	if len(fake.responds) != 1 || len(fake.followups) != 0 {
		t.Fatalf("defer must be exactly one direct response: %+v", fake)
	}
	resp := fake.responds[0]
	if resp.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("response type = %v", resp.Type)
	}
	if resp.Data == nil || resp.Data.Flags != discordgo.MessageFlagsEphemeral {
		t.Fatalf("defer must be ephemeral: %+v", resp.Data)
	}
	if err := deferEphemeralResponse(&fakeInteractionAPI{respondErr: errors.New("boom")}, testInteraction()); err == nil {
		t.Fatal("defer error must propagate")
	}
}

func TestFollowupEphemeral(t *testing.T) {
	fake := &fakeInteractionAPI{}
	if err := followupEphemeral(fake, testInteraction(), "done"); err != nil {
		t.Fatal(err)
	}
	if len(fake.followups) != 1 || len(fake.responds) != 0 {
		t.Fatalf("followup must not use InteractionRespond: %+v", fake)
	}
	if fake.followups[0].Content != "done" || fake.followups[0].Flags != discordgo.MessageFlagsEphemeral {
		t.Fatalf("followup = %+v", fake.followups[0])
	}
	if err := followupEphemeral(&fakeInteractionAPI{followupErr: errors.New("boom")}, testInteraction(), "done"); err == nil {
		t.Fatal("followup error must propagate")
	}
}

func TestFollowupEphemeralComponents(t *testing.T) {
	fake := &fakeInteractionAPI{}
	components := []discordgo.MessageComponent{discordgo.ActionsRow{}}
	if err := followupEphemeralComponents(fake, testInteraction(), "pick:", components); err != nil {
		t.Fatal(err)
	}
	if len(fake.followups) != 1 || len(fake.followups[0].Components) != 1 {
		t.Fatalf("components lost: %+v", fake.followups)
	}
}
