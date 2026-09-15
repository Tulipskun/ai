package discord

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

func wizardCommandInteraction() *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      discordgo.InteractionApplicationCommand,
		ChannelID: "c1",
		Member:    &discordgo.Member{User: &discordgo.User{ID: "u1"}},
		Data:      discordgo.ApplicationCommandInteractionData{Name: "model"},
	}}
}

func openTestWizard(t *testing.T, handler *ModelSettingsHandler, fake *fakeInteractionAPI) {
	t.Helper()
	if err := handler.openWizard(fake, wizardCommandInteraction()); err != nil {
		t.Fatal(err)
	}
}

func wizardComponentInteraction(customID string, values ...string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      discordgo.InteractionMessageComponent,
		ChannelID: "c1",
		Member:    &discordgo.Member{User: &discordgo.User{ID: "u1"}},
		Data: discordgo.MessageComponentInteractionData{
			CustomID:      customID,
			ComponentType: discordgo.SelectMenuComponent,
			Values:        values,
		},
	}}
}

func wizardTestHandler(session *sdk.Session, keys *sdk.KeyPool, models []sdk.Model) *ModelSettingsHandler {
	return &ModelSettingsHandler{
		Providers:    []sdk.ProviderID{"B.ai"},
		ProviderKeys: map[sdk.ProviderID]*sdk.KeyPool{"B.ai": keys},
		Models: func(_ context.Context, _ sdk.ProviderID) ([]sdk.Model, error) {
			return models, nil
		},
		ResolveSession: func(_ context.Context, _ sdk.Input) (*sdk.Session, error) {
			return session, nil
		},
	}
}

func lastEdit(t *testing.T, fake *fakeInteractionAPI) *discordgo.WebhookEdit {
	t.Helper()
	if len(fake.edits) == 0 {
		t.Fatal("expected an edited message")
	}
	return fake.edits[len(fake.edits)-1]
}

func editContent(t *testing.T, edit *discordgo.WebhookEdit) string {
	t.Helper()
	if edit.Content == nil {
		t.Fatal("edited message has no content")
	}
	return *edit.Content
}

func TestProviderMenuListsEveryProvider(t *testing.T) {
	content, components := providerMenuMessage([]sdk.ProviderID{"B.ai", "google"})
	if !strings.Contains(content, "provider") {
		t.Fatalf("content = %q", content)
	}
	if len(components) != 1 {
		t.Fatalf("components = %d, want one provider menu row", len(components))
	}
	row, ok := components[0].(discordgo.ActionsRow)
	if !ok {
		t.Fatalf("component is %T, want ActionsRow", components[0])
	}
	menu, ok := row.Components[0].(discordgo.SelectMenu)
	if !ok || menu.CustomID != modelWizardProvider || len(menu.Options) != 2 {
		t.Fatalf("provider menu = %+v", row.Components[0])
	}
}

func TestModelPagesCoverLargeCatalogues(t *testing.T) {
	models := make([]sdk.Model, 0, 130)
	for index := 1; index <= 130; index++ {
		models = append(models, sdk.Model{ID: "model-" + strconv.Itoa(index)})
	}
	if modelPages(0) != 1 || modelPages(125) != 1 || modelPages(126) != 2 || modelPages(250) != 2 {
		t.Fatalf("page math wrong: 0=%d 125=%d 126=%d 250=%d", modelPages(0), modelPages(125), modelPages(126), modelPages(250))
	}
	content, components := modelPageMessage("B.ai", models, 0, "")
	if !strings.Contains(content, "page 1 of 2") {
		t.Fatalf("content = %q", content)
	}
	menus := 0
	pager := false
	for _, component := range components {
		row := component.(discordgo.ActionsRow)
		for _, child := range row.Components {
			switch child := child.(type) {
			case discordgo.SelectMenu:
				if child.CustomID != modelWizardModel {
					t.Fatalf("model menu custom ID = %q", child.CustomID)
				}
				if len(child.Options) > modelMenuOptions {
					t.Fatalf("menu has %d options", len(child.Options))
				}
				menus++
			case discordgo.Button:
				if child.CustomID == modelWizardPageNext || child.CustomID == modelWizardPagePrev {
					pager = true
				}
			}
		}
	}
	if menus != 5 || !pager {
		t.Fatalf("page 0 must have 5 menus and a pager: menus=%d pager=%v", menus, pager)
	}
	content, components = modelPageMessage("B.ai", models, 5, "")
	if !strings.Contains(content, "page 2 of 2") {
		t.Fatalf("overflow page must clamp to the last page: %q", content)
	}
	menus = 0
	for _, component := range components {
		row := component.(discordgo.ActionsRow)
		for _, child := range row.Components {
			if menu, ok := child.(discordgo.SelectMenu); ok && menu.CustomID == modelWizardModel {
				menus++
				if len(menu.Options) != 5 {
					t.Fatalf("last page menu has %d options", len(menu.Options))
				}
			}
		}
	}
	if menus != 1 {
		t.Fatalf("last page must have 1 menu: %d", menus)
	}
}

func TestTemperatureMenuOffersPresets(t *testing.T) {
	content, components := temperatureMenuMessage("0.7")
	if !strings.Contains(content, "temperature") {
		t.Fatalf("content = %q", content)
	}
	row := components[0].(discordgo.ActionsRow)
	menu := row.Components[0].(discordgo.SelectMenu)
	if len(menu.Options) != len(modelTemperatures) {
		t.Fatalf("options = %d, want presets", len(menu.Options))
	}
	found := false
	for _, option := range menu.Options {
		if option.Value == "0.7" && option.Default {
			found = true
		}
	}
	if !found {
		t.Fatalf("current temperature not preselected: %+v", menu.Options)
	}
}

func TestWizardAppliesEverythingAtTheEnd(t *testing.T) {
	keys := sdk.NewKeyPool("k1", "k2")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys)
	models := []sdk.Model{{ID: "m1"}, {ID: "m2"}}
	handler := wizardTestHandler(session, keys, models)
	fake := &fakeInteractionAPI{}

	command := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      discordgo.InteractionApplicationCommand,
		ChannelID: "c1",
		Member:    &discordgo.Member{User: &discordgo.User{ID: "u1"}},
		Data:      discordgo.ApplicationCommandInteractionData{Name: "model"},
	}}
	if err := handler.openWizard(fake, command); err != nil {
		t.Fatal(err)
	}
	if len(fake.responds) != 1 || fake.responds[0].Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Fatalf("wizard must open immediately: %+v", fake.responds)
	}

	steps := []struct {
		customID string
		values   []string
		wantEdit string
	}{
		{modelWizardProvider, []string{"B.ai"}, "Select a model"},
		{modelWizardModel, []string{"m2"}, "temperature"},
		{modelWizardTemp, []string{"0.7"}, "thinking"},
		{modelWizardThinking, []string{"high"}, "API key pool"},
		{modelWizardKey, []string{"2"}, "Session Model Settings"},
	}
	for _, step := range steps {
		responds, edits := len(fake.responds), len(fake.edits)
		if err := handler.stepWizard(fake, wizardComponentInteraction(step.customID, step.values...)); err != nil {
			t.Fatal(err)
		}
		if len(fake.responds) != responds+1 || fake.responds[len(fake.responds)-1].Type != discordgo.InteractionResponseDeferredMessageUpdate {
			t.Fatalf("%s must defer the component first", step.customID)
		}
		if len(fake.edits) != edits+1 {
			t.Fatalf("%s must rewrite the same message", step.customID)
		}
		if content := editContent(t, lastEdit(t, fake)); !strings.Contains(content, step.wantEdit) {
			t.Fatalf("%s edit = %q, want %q", step.customID, content, step.wantEdit)
		}
	}
	if len(fake.followups) != 0 {
		t.Fatalf("wizard must reuse one message, never send new ones: %+v", fake.followups)
	}
	got := session.Config()
	if got.Provider != "B.ai" || got.Model != "m2" || got.ThinkingLevel != sdk.ThinkingHigh || got.KeyIndex != 1 {
		t.Fatalf("not applied: %+v", got)
	}
	if got.Temperature == nil || *got.Temperature != 0.7 {
		t.Fatalf("temperature not applied: %+v", got.Temperature)
	}
	if _, ok := handler.wizardFor("c1", "u1"); ok {
		t.Fatal("pending wizard must be cleared after apply")
	}
}

func TestWizardBackReturnsOneStage(t *testing.T) {
	keys := sdk.NewKeyPool("k1")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys)
	handler := wizardTestHandler(session, keys, []sdk.Model{{ID: "m1"}})
	fake := &fakeInteractionAPI{}
	openTestWizard(t, handler, fake)
	if err := handler.stepWizard(fake, wizardComponentInteraction(modelWizardProvider, "B.ai")); err != nil {
		t.Fatal(err)
	}
	// From the temperature stage, Back must return to the model list.
	if err := handler.stepWizard(fake, wizardComponentInteraction(modelWizardModel, "m1")); err != nil {
		t.Fatal(err)
	}
	if err := handler.stepWizard(fake, wizardComponentInteraction(modelWizardBack)); err != nil {
		t.Fatal(err)
	}
	if content := editContent(t, lastEdit(t, fake)); !strings.Contains(content, "Select a model") {
		t.Fatalf("back must return to the model list: %q", content)
	}
}

func TestWizardPagerTurnsCataloguePages(t *testing.T) {
	models := make([]sdk.Model, 0, 130)
	for index := 1; index <= 130; index++ {
		models = append(models, sdk.Model{ID: "model-" + strconv.Itoa(index)})
	}
	keys := sdk.NewKeyPool("k1")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys)
	handler := wizardTestHandler(session, keys, models)
	fake := &fakeInteractionAPI{}
	openTestWizard(t, handler, fake)
	if err := handler.stepWizard(fake, wizardComponentInteraction(modelWizardProvider, "B.ai")); err != nil {
		t.Fatal(err)
	}
	// A pick from a user with no pending wizard (for example after a
	// restart) must fail loudly and immediately instead of acting stale.
	other := wizardComponentInteraction(modelWizardModel, "model-1")
	other.ChannelID = "other"
	other.Member.User.ID = "u9"
	responds, edits := len(fake.responds), len(fake.edits)
	if err := handler.stepWizard(fake, other); err != nil {
		t.Fatal(err)
	}
	if len(fake.responds) != responds+1 || len(fake.edits) != edits {
		t.Fatalf("stale pick must answer immediately without editing: %+v %+v", fake.responds, fake.edits)
	}
	if content := fake.responds[len(fake.responds)-1].Data.Content; !strings.Contains(content, "expired") {
		t.Fatalf("stale pick must say the setup expired: %q", content)
	}
	if err := handler.stepWizard(fake, wizardComponentInteraction(modelWizardPageNext)); err != nil {
		t.Fatal(err)
	}
	if content := editContent(t, lastEdit(t, fake)); !strings.Contains(content, "page 2 of 2") {
		t.Fatalf("pager must turn the page: %q", content)
	}
	if err := handler.stepWizard(fake, wizardComponentInteraction(modelWizardPagePrev)); err != nil {
		t.Fatal(err)
	}
	if content := editContent(t, lastEdit(t, fake)); !strings.Contains(content, "page 1 of 2") {
		t.Fatalf("pager must turn back: %q", content)
	}
}

func TestWizardExpiredPendingFailsLoudly(t *testing.T) {
	keys := sdk.NewKeyPool("k1")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys)
	handler := wizardTestHandler(session, keys, []sdk.Model{{ID: "m1"}})
	fake := &fakeInteractionAPI{}
	if err := handler.stepWizard(fake, wizardComponentInteraction(modelWizardModel, "m1")); err != nil {
		t.Fatal(err)
	}
	if len(fake.responds) != 1 || len(fake.edits) != 0 {
		t.Fatalf("expired wizard must answer immediately: %+v %+v", fake.responds, fake.edits)
	}
	if got := fake.responds[0]; got.Type != discordgo.InteractionResponseChannelMessageWithSource || !strings.Contains(got.Data.Content, "expired") {
		t.Fatalf("expired wizard must say the setup expired: %+v", got.Data)
	}
}

func TestApplySetupAppliesEveryField(t *testing.T) {
	session, keys := setupApplySession(t)
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

func setupApplySession(t *testing.T) (*sdk.Session, *sdk.KeyPool) {
	t.Helper()
	keys := sdk.NewKeyPool("k1", "k2")
	return sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys), keys
}

func TestApplySetupRejectsBadInput(t *testing.T) {
	models := []sdk.Model{{ID: "m1"}, {ID: "m2"}}
	base := map[string]string{"model": "m1", "temperature": "", "thinking": "default", "key": "1"}
	cases := map[string]map[string]string{
		"missing model":   {"temperature": "", "thinking": "default", "key": "1"},
		"unknown model":   {"model": "nope", "temperature": "", "thinking": "default", "key": "1"},
		"bad temperature": {"model": "m1", "temperature": "9", "thinking": "default", "key": "1"},
		"bad thinking":    {"model": "m1", "temperature": "", "thinking": "ultra", "key": "1"},
		"bad key":         {"model": "m1", "temperature": "", "thinking": "default", "key": "0"},
		"key overflow":    {"model": "m1", "temperature": "", "thinking": "default", "key": "7"},
	}
	for name, values := range cases {
		session, keys := setupApplySession(t)
		if err := applyModelSetup(session, keys, "B.ai", values, models); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	session, _ := setupApplySession(t)
	if err := applyModelSetup(session, nil, "B.ai", base, models); err == nil {
		t.Fatal("missing key pool was accepted")
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
