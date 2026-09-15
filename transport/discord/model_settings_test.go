package discord

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

func modalLabels(t *testing.T, data *discordgo.InteractionResponseData) []discordgo.Label {
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

func TestStep1ModalSelectsProviderAndFilters(t *testing.T) {
	data := modelStep1Modal([]sdk.ProviderID{"B.ai", "google"})
	if data.CustomID != modelStep1ModalID {
		t.Fatalf("custom ID = %q", data.CustomID)
	}
	labels := modalLabels(t, data)
	if len(labels) != 2 {
		t.Fatalf("components = %d, want provider select + filter", len(labels))
	}
	provider, ok := labels[0].Component.(discordgo.SelectMenu)
	if !ok || provider.CustomID != "provider" || len(provider.Options) != 2 {
		t.Fatalf("provider select = %+v", labels[0].Component)
	}
	filter, ok := labels[1].Component.(discordgo.TextInput)
	if !ok || filter.CustomID != "filter" || filter.Required == nil || *filter.Required {
		t.Fatalf("filter input must be optional text: %+v", labels[1].Component)
	}
}

func TestFilterModelsNarrowsCatalog(t *testing.T) {
	models := []sdk.Model{{ID: "qwen3.8-flash"}, {ID: "qwen3.5-plus"}, {ID: "gpt-5"}}
	if got := filterModels(models, ""); len(got) != 3 {
		t.Fatalf("blank filter = %d, want all", len(got))
	}
	got := filterModels(models, "QWEN")
	if len(got) != 2 {
		t.Fatalf("filter = %v, want 2 qwen models", got)
	}
	if got := filterModels(models, "nope"); len(got) != 0 {
		t.Fatalf("filter = %v, want none", got)
	}
}

func emptyTestConfig() sdk.SessionConfig {
	return sdk.SessionConfig{ID: "discord:channel:c1", Provider: "B.ai", Model: "old", KeyIndex: 0}
}

func TestStep2ModalFitsOneModal(t *testing.T) {
	models := make([]sdk.Model, 0, 40)
	for index := 1; index <= 40; index++ {
		models = append(models, sdk.Model{ID: "model-" + strconv.Itoa(index)})
	}
	data, err := modelStep2Modal("c1", "B.ai", models, emptyTestConfig(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(data.CustomID, modelStep2Prefix) {
		t.Fatalf("custom ID = %q", data.CustomID)
	}
	labels := modalLabels(t, data)
	if len(labels) != 5 {
		t.Fatalf("components = %d, want 2 model menus + temperature + thinking + key", len(labels))
	}
	seen := map[string]bool{}
	options := 0
	for _, label := range labels {
		menu, ok := label.Component.(discordgo.SelectMenu)
		if !ok {
			continue
		}
		if strings.HasPrefix(menu.CustomID, "model_") {
			seen[menu.CustomID] = true
			options += len(menu.Options)
		}
	}
	if len(seen) != 2 || options != 40 {
		t.Fatalf("model menus = %v with %d options", seen, options)
	}
}

func TestStep2ModalRejectsEmptyAndOversized(t *testing.T) {
	if _, err := modelStep2Modal("c1", "B.ai", nil, emptyTestConfig(), 1); err == nil {
		t.Fatal("empty catalogue must error")
	}
	big := make([]sdk.Model, 0, maxStep2Models+1)
	for index := 0; index <= maxStep2Models; index++ {
		big = append(big, sdk.Model{ID: "m-" + strconv.Itoa(index)})
	}
	if _, err := modelStep2Modal("c1", "B.ai", big, emptyTestConfig(), 1); err == nil || !strings.Contains(err.Error(), "narrow the filter") {
		t.Fatalf("oversized catalogue must ask for a narrower filter: %v", err)
	}
}

func TestStep2ModalPreselectsCurrentValues(t *testing.T) {
	temp := 0.7
	config := sdk.SessionConfig{ID: "c", Provider: "B.ai", Model: "m2", Temperature: &temp, ThinkingLevel: sdk.ThinkingHigh, KeyIndex: 1}
	data, err := modelStep2Modal("c1", "B.ai", []sdk.Model{{ID: "m1"}, {ID: "m2"}}, config, 2)
	if err != nil {
		t.Fatal(err)
	}
	foundModel, foundKey := false, false
	for _, label := range modalLabels(t, data) {
		switch child := label.Component.(type) {
		case discordgo.SelectMenu:
			for _, option := range child.Options {
				if child.CustomID == "model_0" && option.Value == "m2" && option.Default {
					foundModel = true
				}
				if child.CustomID == "key" && option.Value == "2" && option.Default {
					foundKey = true
				}
			}
		case discordgo.TextInput:
			if child.CustomID == "temperature" && child.Value != "0.7" {
				t.Fatalf("temperature value = %q", child.Value)
			}
		}
	}
	if !foundModel || !foundKey {
		t.Fatalf("current model/key not preselected (model=%v key=%v)", foundModel, foundKey)
	}
}

func applyStep2TestSession(t *testing.T) (*sdk.Session, *sdk.KeyPool) {
	t.Helper()
	keys := sdk.NewKeyPool("k1", "k2")
	return sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys), keys
}

func TestApplyStep2AppliesEveryField(t *testing.T) {
	session, keys := applyStep2TestSession(t)
	models := []sdk.Model{{ID: "m1"}, {ID: "m2"}}
	values := map[string]string{"model_1": "m2", "temperature": "0.7", "thinking": "high", "key": "2"}
	if err := applyModelStep2(session, keys, "B.ai", values, models); err != nil {
		t.Fatal(err)
	}
	got := session.Config()
	if got.Provider != "B.ai" || got.Model != "m2" || got.ThinkingLevel != sdk.ThinkingHigh || got.KeyIndex != 1 {
		t.Fatalf("not applied: %+v", got)
	}
	if got.Temperature == nil || *got.Temperature != 0.7 {
		t.Fatalf("temperature not applied: %+v", got.Temperature)
	}
	if summary := sessionSettingsSummary(got); !strings.Contains(summary, "m2") || !strings.Contains(summary, "API Pool: `2`") {
		t.Fatalf("summary missing applied values: %s", summary)
	}
}

func TestApplyStep2RejectsBadInput(t *testing.T) {
	models := []sdk.Model{{ID: "m1"}}
	base := map[string]string{"model_0": "m1", "temperature": "", "thinking": "default", "key": "1"}
	cases := map[string]map[string]string{
		"missing model":   {"temperature": "", "thinking": "default", "key": "1"},
		"unknown model":   {"model_0": "nope", "temperature": "", "thinking": "default", "key": "1"},
		"bad temperature": {"model_0": "m1", "temperature": "9", "thinking": "default", "key": "1"},
		"bad thinking":    {"model_0": "m1", "temperature": "", "thinking": "ultra", "key": "1"},
		"bad key":         {"model_0": "m1", "temperature": "", "thinking": "default", "key": "0"},
	}
	for name, values := range cases {
		session, keys := applyStep2TestSession(t)
		if err := applyModelStep2(session, keys, "B.ai", values, models); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	session, _ := applyStep2TestSession(t)
	if err := applyModelStep2(session, nil, "B.ai", base, models); err == nil {
		t.Fatal("missing key pool was accepted")
	}
}

func TestModelInCatalog(t *testing.T) {
	models := []sdk.Model{{ID: "m1"}, {ID: "m2"}}
	if !modelInCatalog(models, "m1") { t.Fatal("expected m1 in catalog") }
	if modelInCatalog(models, "m3") { t.Fatal("did not expect m3 in catalog") }
}

func step2SubmitInteraction(values map[string]string) *discordgo.InteractionCreate {
	components := []discordgo.MessageComponent{
		discordgo.Label{Label: "Model", Component: discordgo.SelectMenu{CustomID: "model_0", MenuType: discordgo.StringSelectMenu, Values: []string{values["model_0"]}}},
		discordgo.Label{Label: "Temperature", Component: discordgo.TextInput{CustomID: "temperature", Value: values["temperature"]}},
		discordgo.Label{Label: "Thinking", Component: discordgo.SelectMenu{CustomID: "thinking", MenuType: discordgo.StringSelectMenu, Values: []string{values["thinking"]}}},
		discordgo.Label{Label: "API Pool", Component: discordgo.SelectMenu{CustomID: "key", MenuType: discordgo.StringSelectMenu, Values: []string{values["key"]}}},
	}
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      discordgo.InteractionModalSubmit,
		ChannelID: "c1",
		Data:      discordgo.ModalSubmitInteractionData{CustomID: "model:step2:c1:B.ai", Components: components},
	}}
}

func step2SubmitHandler(session *sdk.Session, keys *sdk.KeyPool) *ModelSettingsHandler {
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

func TestSubmitStep2DefersThenFollowsUpSummary(t *testing.T) {
	keys := sdk.NewKeyPool("k1", "k2")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys)
	handler := step2SubmitHandler(session, keys)
	fake := &fakeInteractionAPI{}
	values := map[string]string{"model_0": "m2", "temperature": "0.7", "thinking": "high", "key": "2"}
	if err := handler.submitStep2(fake, step2SubmitInteraction(values), "model:step2:c1:B.ai"); err != nil {
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

func TestSubmitStep2FailureFollowsUpError(t *testing.T) {
	keys := sdk.NewKeyPool("k1")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys)
	handler := step2SubmitHandler(session, keys)
	fake := &fakeInteractionAPI{}
	values := map[string]string{"model_0": "nope", "temperature": "", "thinking": "default", "key": "1"}
	if err := handler.submitStep2(fake, step2SubmitInteraction(values), "model:step2:c1:B.ai"); err != nil {
		t.Fatal(err)
	}
	if len(fake.responds) != 1 || len(fake.followups) != 1 {
		t.Fatalf("failure must defer then follow up: %+v %+v", fake.responds, fake.followups)
	}
	if !strings.Contains(fake.followups[0].Content, "not available") {
		t.Fatalf("followup must carry the error: %s", fake.followups[0].Content)
	}
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
