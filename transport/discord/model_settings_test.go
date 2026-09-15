package discord

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

func setupModalLabels(t *testing.T, data *discordgo.InteractionResponseData) []discordgo.Label {
	t.Helper()
	labels := make([]discordgo.Label, 0, len(data.Components))
	for _, component := range data.Components {
		label, ok := component.(discordgo.Label)
		if !ok {
			t.Fatalf("modal component is %T, want discordgo.Label", component)
		}
		labels = append(labels, label)
	}
	return labels
}

func TestSetupModalHasFiveFieldsWithCurrentValues(t *testing.T) {
	temp := 0.7
	config := sdk.SessionConfig{ID: "c", Provider: "B.ai", Model: "m2", Temperature: &temp, ThinkingLevel: sdk.ThinkingHigh, KeyIndex: 1}
	data := modelSetupModal([]sdk.ProviderID{"B.ai", "google"}, config)
	if data.CustomID != modelSetupModalID {
		t.Fatalf("custom ID = %q", data.CustomID)
	}
	labels := setupModalLabels(t, data)
	if len(labels) != 5 {
		t.Fatalf("components = %d, want provider + model + temperature + thinking + key", len(labels))
	}
	byID := map[string]discordgo.Label{}
	for _, label := range labels {
		switch child := label.Component.(type) {
		case discordgo.SelectMenu:
			byID[child.CustomID] = label
		case discordgo.TextInput:
			byID[child.CustomID] = label
		default:
			t.Fatalf("unexpected component %T", label.Component)
		}
	}
	for _, want := range []string{"provider", "model", "temperature", "thinking", "key"} {
		if _, ok := byID[want]; !ok {
			t.Fatalf("modal missing %q", want)
		}
	}
	provider := byID["provider"].Component.(discordgo.SelectMenu)
	foundDefault := false
	for _, option := range provider.Options {
		if option.Value == "B.ai" && option.Default {
			foundDefault = true
		}
	}
	if !foundDefault || len(provider.Options) != 2 {
		t.Fatalf("provider options = %+v", provider.Options)
	}
	if model := byID["model"].Component.(discordgo.TextInput); model.Value != "m2" {
		t.Fatalf("model value = %q", model.Value)
	}
	if temperature := byID["temperature"].Component.(discordgo.TextInput); temperature.Value != "0.7" {
		t.Fatalf("temperature value = %q", temperature.Value)
	}
}

func TestResolveModelIDAcceptsExactAndUniqueSubstring(t *testing.T) {
	models := []sdk.Model{{ID: "qwen3.8-flash"}, {ID: "qwen3.5-plus"}, {ID: "gpt-5"}}
	if got, err := resolveModelID(models, "B.ai", "gpt-5"); err != nil || got != "gpt-5" {
		t.Fatalf("exact = %q, %v", got, err)
	}
	if got, err := resolveModelID(models, "B.ai", "3.8-flash"); err != nil || got != "qwen3.8-flash" {
		t.Fatalf("unique substring = %q, %v", got, err)
	}
	if _, err := resolveModelID(models, "B.ai", ""); err == nil {
		t.Fatal("blank model was accepted")
	}
	if _, err := resolveModelID(models, "B.ai", "qwen"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous input must list matches: %v", err)
	} else if !strings.Contains(err.Error(), "qwen3.8-flash") || !strings.Contains(err.Error(), "qwen3.5-plus") {
		t.Fatalf("ambiguous error must list matches: %v", err)
	}
	if _, err := resolveModelID(models, "B.ai", "nope"); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("unknown model must error: %v", err)
	}
}

func setupTestSession(t *testing.T) (*sdk.Session, *sdk.KeyPool) {
	t.Helper()
	keys := sdk.NewKeyPool("k1", "k2")
	return sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys), keys
}

func TestApplySetupAppliesEveryField(t *testing.T) {
	session, keys := setupTestSession(t)
	models := []sdk.Model{{ID: "m1"}, {ID: "m2"}}
	values := map[string]string{"model": "m2", "temperature": "0.7", "thinking": "high", "key": "2"}
	if err := applyModelSetup(session, keys, "B.ai", values, models); err != nil {
		t.Fatal(err)
	}
	got := session.Config()
	if got.Provider != "B.ai" || got.Model != "m2" || got.ThinkingLevel != sdk.ThinkingHigh || got.KeyIndex != 1 {
		t.Fatalf("not applied: %+v", got)
	}
	if got.Temperature == nil || *got.Temperature != 0.7 {
		t.Fatalf("temperature not applied: %+v", got.Temperature)
	}
}

func TestApplySetupKeepsKeyIndexWhenBlank(t *testing.T) {
	session, keys := setupTestSession(t)
	if err := session.SetProvider("B.ai", keys); err != nil {
		t.Fatal(err)
	}
	if err := session.SetKeyIndex(1); err != nil {
		t.Fatal(err)
	}
	models := []sdk.Model{{ID: "m1"}}
	values := map[string]string{"model": "m1", "temperature": "", "thinking": "default", "key": ""}
	if err := applyModelSetup(session, keys, "B.ai", values, models); err != nil {
		t.Fatal(err)
	}
	if got := session.Config(); got.KeyIndex != 1 {
		t.Fatalf("blank key must keep pool index: %+v", got)
	}
}

func TestApplySetupRejectsBadInput(t *testing.T) {
	models := []sdk.Model{{ID: "m1"}, {ID: "m2"}}
	base := map[string]string{"model": "m1", "temperature": "", "thinking": "default", "key": "1"}
	cases := map[string]map[string]string{
		"missing model":   {"temperature": "", "thinking": "default", "key": "1"},
		"unknown model":   {"model": "nope", "temperature": "", "thinking": "default", "key": "1"},
		"ambiguous model": {"model": "m", "temperature": "", "thinking": "default", "key": "1"},
		"bad temperature": {"model": "m1", "temperature": "9", "thinking": "default", "key": "1"},
		"bad thinking":    {"model": "m1", "temperature": "", "thinking": "ultra", "key": "1"},
		"bad key":         {"model": "m1", "temperature": "", "thinking": "default", "key": "0"},
		"key overflow":    {"model": "m1", "temperature": "", "thinking": "default", "key": "7"},
	}
	for name, values := range cases {
		session, keys := setupTestSession(t)
		if err := applyModelSetup(session, keys, "B.ai", values, models); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	session, _ := setupTestSession(t)
	if err := applyModelSetup(session, nil, "B.ai", base, models); err == nil {
		t.Fatal("missing key pool was accepted")
	}
}

func setupSubmitInteraction(values map[string]string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      discordgo.InteractionModalSubmit,
		ChannelID: "c1",
		Data: discordgo.ModalSubmitInteractionData{CustomID: modelSetupModalID, Components: []discordgo.MessageComponent{
			discordgo.Label{Label: "Provider", Component: discordgo.SelectMenu{CustomID: "provider", MenuType: discordgo.StringSelectMenu, Values: []string{values["provider"]}}},
			discordgo.Label{Label: "Model", Component: discordgo.TextInput{CustomID: "model", Value: values["model"]}},
			discordgo.Label{Label: "Temperature", Component: discordgo.TextInput{CustomID: "temperature", Value: values["temperature"]}},
			discordgo.Label{Label: "Thinking", Component: discordgo.SelectMenu{CustomID: "thinking", MenuType: discordgo.StringSelectMenu, Values: []string{values["thinking"]}}},
			discordgo.Label{Label: "API Pool", Component: discordgo.TextInput{CustomID: "key", Value: values["key"]}},
		}},
	}}
}

func setupSubmitHandler(session *sdk.Session, keys *sdk.KeyPool) *ModelSettingsHandler {
	return &ModelSettingsHandler{
		Providers:    []sdk.ProviderID{"B.ai"},
		ProviderKeys: map[sdk.ProviderID]*sdk.KeyPool{"B.ai": keys},
		Models: func(_ context.Context, _ sdk.ProviderID) ([]sdk.Model, error) {
			return []sdk.Model{{ID: "m1"}, {ID: "m2"}}, nil
		},
		ResolveSession: func(_ context.Context, _ sdk.Input) (*sdk.Session, error) {
			return session, nil
		},
	}
}

func TestSubmitSetupDefersThenFollowsUpSummary(t *testing.T) {
	keys := sdk.NewKeyPool("k1", "k2")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys)
	handler := setupSubmitHandler(session, keys)
	fake := &fakeInteractionAPI{}
	values := map[string]string{"provider": "B.ai", "model": "m2", "temperature": "0.7", "thinking": "high", "key": "2"}
	if err := handler.submitSetup(fake, setupSubmitInteraction(values)); err != nil {
		t.Fatal(err)
	}
	if len(fake.responds) != 1 || fake.responds[0].Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("submit must defer first: %+v", fake.responds)
	}
	if len(fake.followups) != 1 {
		t.Fatalf("submit outcome must be a followup: %+v", fake.followups)
	}
	for _, want := range []string{"m2", "0.7", "high", "API Pool: `2`"} {
		if !strings.Contains(fake.followups[0].Content, want) {
			t.Fatalf("summary missing %q: %s", want, fake.followups[0].Content)
		}
	}
	if got := session.Config(); got.Model != "m2" || got.KeyIndex != 1 {
		t.Fatalf("settings not applied: %+v", got)
	}
}

func TestSubmitSetupFailureFollowsUpError(t *testing.T) {
	keys := sdk.NewKeyPool("k1")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys)
	handler := setupSubmitHandler(session, keys)
	fake := &fakeInteractionAPI{}
	values := map[string]string{"provider": "B.ai", "model": "nope", "temperature": "", "thinking": "default", "key": "1"}
	if err := handler.submitSetup(fake, setupSubmitInteraction(values)); err != nil {
		t.Fatal(err)
	}
	if len(fake.responds) != 1 || len(fake.followups) != 1 {
		t.Fatalf("failure must defer then follow up: %+v %+v", fake.responds, fake.followups)
	}
	if !strings.Contains(fake.followups[0].Content, "not available") {
		t.Fatalf("followup must carry the error: %s", fake.followups[0].Content)
	}
}

func TestSubmitSetupRejectsUnknownProviderImmediately(t *testing.T) {
	keys := sdk.NewKeyPool("k1")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys)
	handler := setupSubmitHandler(session, keys)
	fake := &fakeInteractionAPI{}
	values := map[string]string{"provider": "nope", "model": "m1", "temperature": "", "thinking": "default", "key": "1"}
	if err := handler.submitSetup(fake, setupSubmitInteraction(values)); err != nil {
		t.Fatal(err)
	}
	if len(fake.responds) != 1 || len(fake.followups) != 0 {
		t.Fatalf("fast validation must answer immediately: %+v %+v", fake.responds, fake.followups)
	}
	if !strings.Contains(fake.responds[0].Data.Content, "unknown provider") {
		t.Fatalf("response must name the problem: %+v", fake.responds[0].Data)
	}
}

func TestModelInCatalog(t *testing.T) {
	models := []sdk.Model{{ID: "m1"}, {ID: "m2"}}
	if !modelInCatalog(models, "m1") { t.Fatal("expected m1 in catalog") }
	if modelInCatalog(models, "m3") { t.Fatal("did not expect m3 in catalog") }
}

func TestSessionIDForChannelUsesMapping(t *testing.T) {
	h := &ModelSettingsHandler{SessionForChannel: func(channelID string) string {
		if channelID == "c1" {
			return "custom-session-9"
		}
		return ""
	}}
	if got := h.sessionIDFor("c1"); got != "custom-session-9" {
		t.Fatalf("mapped session = %q", got)
	}
	if got := h.sessionIDFor("c2"); got != "discord:channel:c2" {
		t.Fatalf("unmapped fallback = %q", got)
	}
	var nilHandler *ModelSettingsHandler
	if got := nilHandler.sessionIDFor("c1"); got != "discord:channel:c1" {
		t.Fatalf("nil handler fallback = %q", got)
	}
	if got := (&ModelSettingsHandler{}).sessionIDFor("c1"); got != "discord:channel:c1" {
		t.Fatalf("missing mapping fallback = %q", got)
	}
}
