package discord

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

// fakeNewChannelDiscord records /new Discord calls without any network.
type fakeNewChannelDiscord struct {
	createErr  error
	sendErr    error
	guildID    string
	name       string
	ctype      discordgo.ChannelType
	newID      string
	sentTo     string
	sentText   string
	deferred   bool
	calls      []string
	ephemerals []string
	followups  []string
}

func (f *fakeNewChannelDiscord) GuildChannelCreate(guildID, name string, ctype discordgo.ChannelType, _ ...discordgo.RequestOption) (*discordgo.Channel, error) {
	f.guildID, f.name, f.ctype = guildID, name, ctype
	f.calls = append(f.calls, "create")
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &discordgo.Channel{ID: f.newID, Name: name, Type: ctype}, nil
}

func (f *fakeNewChannelDiscord) InteractionRespond(_ *discordgo.Interaction, resp *discordgo.InteractionResponse, _ ...discordgo.RequestOption) error {
	if resp == nil || resp.Data == nil {
		return errors.New("empty response")
	}
	if resp.Type == discordgo.InteractionResponseDeferredChannelMessageWithSource {
		f.deferred = true
		f.calls = append(f.calls, "defer")
		return nil
	}
	f.ephemerals = append(f.ephemerals, resp.Data.Content)
	return nil
}

func (f *fakeNewChannelDiscord) FollowupMessageCreate(_ *discordgo.Interaction, _ bool, data *discordgo.WebhookParams, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	f.followups = append(f.followups, data.Content)
	f.calls = append(f.calls, "followup")
	return &discordgo.Message{ID: "msg-1"}, nil
}

func (f *fakeNewChannelDiscord) InteractionResponseEdit(_ *discordgo.Interaction, _ *discordgo.WebhookEdit, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	return &discordgo.Message{ID: "msg-1"}, nil
}

func (f *fakeNewChannelDiscord) ChannelMessageSend(channelID, content string, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	f.sentTo, f.sentText = channelID, content
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	return &discordgo.Message{ID: "msg-1", ChannelID: channelID}, nil
}

func newChannelTestSessions(t *testing.T) (source, target *sdk.Session, resolve func(context.Context, sdk.Input) (*sdk.Session, error), keys *sdk.KeyPool) {
	t.Helper()
	keys = sdk.NewKeyPool("k1", "k2")
	source = sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:src"}, keys)
	if err := source.SetProvider("B.ai", keys); err != nil {
		t.Fatal(err)
	}
	if err := source.SetModel("qwen3.8-flash"); err != nil {
		t.Fatal(err)
	}
	if err := source.SetTemperature(0.7); err != nil {
		t.Fatal(err)
	}
	if err := source.SetThinkingLevel(sdk.ThinkingMedium); err != nil {
		t.Fatal(err)
	}
	if err := source.SetKeyIndex(1); err != nil {
		t.Fatal(err)
	}
	if err := source.SetAgentMode(sdk.AgentModeSub); err != nil {
		t.Fatal(err)
	}
	target = sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:chan-new"}, keys)
	sourceSub := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:src:sub"}, keys)
	if err := sourceSub.SetProvider("C.ai", keys); err != nil {
		t.Fatal(err)
	}
	if err := sourceSub.SetModel("c1"); err != nil {
		t.Fatal(err)
	}
	if err := sourceSub.SetThinkingLevel(sdk.ThinkingLow); err != nil {
		t.Fatal(err)
	}
	if err := sourceSub.SetKeyIndex(1); err != nil {
		t.Fatal(err)
	}
	if err := sourceSub.SetAgentMode(sdk.AgentModeSub); err != nil {
		t.Fatal(err)
	}
	targetSub := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:chan-new:sub"}, keys)
	sessions := map[string]*sdk.Session{
		"discord:channel:src":          source,
		"discord:channel:src:sub":      sourceSub,
		"discord:channel:chan-new":     target,
		"discord:channel:chan-new:sub": targetSub,
	}
	return source, target, func(_ context.Context, input sdk.Input) (*sdk.Session, error) {
		session, ok := sessions[input.SessionID]
		if !ok {
			return nil, errors.New("sdk: sub-agent job not found")
		}
		return session, nil
	}, keys
}

func newChannelInteraction(guildID, channelID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      discordgo.InteractionApplicationCommand,
		GuildID:   guildID,
		ChannelID: channelID,
		Data:      discordgo.ApplicationCommandInteractionData{Name: "new"},
	}}
}

func TestNewChannelNameUsesDateAndTime(t *testing.T) {
	got := newChannelName(time.Date(2026, 9, 15, 14, 30, 0, 0, time.UTC))
	if got != "ai-2026-09-15-1430" {
		t.Fatalf("name = %q", got)
	}
	for _, r := range got {
		if r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			t.Fatalf("name has disallowed rune %q: %s", r, got)
		}
	}
}

func TestNewChannelClonesSettingsAndReports(t *testing.T) {
	_, target, resolve, keys := newChannelTestSessions(t)
	fake := &fakeNewChannelDiscord{newID: "chan-new"}
	handler := &NewChannelHandler{
		ResolveSession: resolve,
		ProviderKeys:   map[sdk.ProviderID]*sdk.KeyPool{"B.ai": keys, "C.ai": keys},
		Now:            func() time.Time { return time.Date(2026, 9, 15, 14, 30, 0, 0, time.UTC) },
	}
	if err := handler.Handle(fake, newChannelInteraction("guild-1", "src")); err != nil {
		t.Fatal(err)
	}
	if fake.guildID != "guild-1" || fake.name != "ai-2026-09-15-1430" || fake.ctype != discordgo.ChannelTypeGuildText {
		t.Fatalf("channel not created in guild with dated name: %+v", fake)
	}
	// The defer must precede the slow channel creation, and the outcome must
	// arrive as a follow-up rather than a second direct response.
	wantCalls := []string{"defer", "create", "followup"}
	if len(fake.calls) != len(wantCalls) {
		t.Fatalf("calls = %v, want %v", fake.calls, wantCalls)
	}
	for index, want := range wantCalls {
		if fake.calls[index] != want {
			t.Fatalf("calls = %v, want %v", fake.calls, wantCalls)
		}
	}
	if len(fake.ephemerals) != 0 {
		t.Fatalf("outcome must use followup, not a direct response: %v", fake.ephemerals)
	}
	got := target.Config()
	if got.Provider != "B.ai" || got.Model != "qwen3.8-flash" || got.ThinkingLevel != sdk.ThinkingMedium || got.KeyIndex != 1 || got.AgentMode != sdk.AgentModeSub {
		t.Fatalf("settings not cloned: %+v", got)
	}
	targetSub, err := resolve(context.Background(), sdk.Input{SessionID: "discord:channel:chan-new:sub"})
	if err != nil {
		t.Fatal(err)
	}
	subGot := targetSub.Config()
	if subGot.Provider != "C.ai" || subGot.Model != "c1" || subGot.KeyIndex != 1 || subGot.ThinkingLevel != sdk.ThinkingLow || subGot.AgentMode != sdk.AgentModeSub {
		t.Fatalf("sub settings not cloned: %+v", subGot)
	}
	if key, err := targetSub.APIKey(); err != nil || key != "k2" {
		t.Fatalf("target sub pool wrong: %q %v", key, err)
	}
	if got.Temperature == nil || *got.Temperature != 0.7 {
		t.Fatalf("temperature not cloned: %+v", got.Temperature)
	}
	if fake.sentTo != "chan-new" {
		t.Fatalf("summary not sent to new channel: %+v", fake)
	}
	for _, want := range []string{"B.ai", "qwen3.8-flash", "medium", "0.7", "API Pool: `2`", "**Main agent**", "**Sub agent**", "C.ai", "c1"} {
		if !strings.Contains(fake.sentText, want) {
			t.Fatalf("summary missing %q: %s", want, fake.sentText)
		}
	}
	if len(fake.followups) != 1 || !strings.Contains(fake.followups[0], "<#chan-new>") {
		t.Fatalf("ack missing new channel mention: %v", fake.followups)
	}
}

func TestNewChannelRejectsDirectMessages(t *testing.T) {
	_, _, resolve, keys := newChannelTestSessions(t)
	fake := &fakeNewChannelDiscord{newID: "chan-new"}
	handler := &NewChannelHandler{ResolveSession: resolve, ProviderKeys: map[sdk.ProviderID]*sdk.KeyPool{"B.ai": keys}}
	if err := handler.Handle(fake, newChannelInteraction("", "src")); err != nil {
		t.Fatal(err)
	}
	if fake.name != "" {
		t.Fatal("channel must not be created from a direct message")
	}
	if fake.deferred {
		t.Fatal("fast validation failures must answer immediately without deferring")
	}
	if len(fake.ephemerals) != 1 || !strings.Contains(fake.ephemerals[0], "server") {
		t.Fatalf("expected guild guidance: %v", fake.ephemerals)
	}
}

func TestNewChannelRequiresSourceSettings(t *testing.T) {
	keys := sdk.NewKeyPool("k1")
	empty := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:src"}, keys)
	resolve := func(_ context.Context, input sdk.Input) (*sdk.Session, error) {
		if input.SessionID != "discord:channel:src" {
			t.Fatalf("must not resolve a new session before source settings are validated: %s", input.SessionID)
		}
		return empty, nil
	}
	fake := &fakeNewChannelDiscord{newID: "chan-new"}
	handler := &NewChannelHandler{ResolveSession: resolve, ProviderKeys: map[sdk.ProviderID]*sdk.KeyPool{"B.ai": keys}}
	if err := handler.Handle(fake, newChannelInteraction("guild-1", "src")); err != nil {
		t.Fatal(err)
	}
	if fake.name != "" || fake.sentTo != "" {
		t.Fatal("nothing must be created when the source has no settings")
	}
	if fake.deferred {
		t.Fatal("fast validation failures must answer immediately without deferring")
	}
	if len(fake.ephemerals) != 1 || !strings.Contains(fake.ephemerals[0], "/model") {
		t.Fatalf("expected /model guidance: %v", fake.ephemerals)
	}
}

func TestNewChannelCreateFailureStaysEphemeral(t *testing.T) {
	_, _, resolve, keys := newChannelTestSessions(t)
	fake := &fakeNewChannelDiscord{createErr: errors.New("403")}
	handler := &NewChannelHandler{ResolveSession: resolve, ProviderKeys: map[sdk.ProviderID]*sdk.KeyPool{"B.ai": keys, "C.ai": keys}}
	if err := handler.Handle(fake, newChannelInteraction("guild-1", "src")); err != nil {
		t.Fatal(err)
	}
	if fake.sentTo != "" {
		t.Fatal("no summary may be sent when creation failed")
	}
	if !fake.deferred || len(fake.followups) != 1 || !strings.Contains(fake.followups[0], "Manage Channels") {
		t.Fatalf("expected deferred permission guidance: deferred=%v followups=%v", fake.deferred, fake.followups)
	}
}

func TestNewChannelIgnoresOtherCommands(t *testing.T) {
	handler := &NewChannelHandler{}
	other := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      discordgo.InteractionApplicationCommand,
		GuildID:   "guild-1",
		ChannelID: "src",
		Data:      discordgo.ApplicationCommandInteractionData{Name: "model"},
	}}
	if err := handler.Handle(&fakeNewChannelDiscord{}, other); err != nil {
		t.Fatal(err)
	}
	if err := handler.Handle(nil, nil); err != nil {
		t.Fatal(err)
	}
}
