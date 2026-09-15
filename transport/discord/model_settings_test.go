package discord

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

func panelCommandInteraction() *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      discordgo.InteractionApplicationCommand,
		ChannelID: "c1",
		Member:    &discordgo.Member{User: &discordgo.User{ID: "u1"}},
		Data:      discordgo.ApplicationCommandInteractionData{Name: "model"},
	}}
}

func panelComponentInteraction(customID string, values ...string) *discordgo.InteractionCreate {
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

func panelButtonInteraction(customID string) *discordgo.InteractionCreate {
	interaction := panelComponentInteraction(customID)
	interaction.Data = discordgo.MessageComponentInteractionData{
		CustomID:      customID,
		ComponentType: discordgo.ButtonComponent,
	}
	return interaction
}

func panelTestHandler(session *sdk.Session, keys *sdk.KeyPool, models []sdk.Model) *ModelSettingsHandler {
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

func editOptionValues(t *testing.T, edit *discordgo.WebhookEdit, customID string) []string {
	t.Helper()
	if edit.Components == nil {
		t.Fatal("edited message has no components")
	}
	for _, component := range *edit.Components {
		row, ok := component.(discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, child := range row.Components {
			var menu *discordgo.SelectMenu
			switch child := child.(type) {
			case discordgo.SelectMenu:
				menu = &child
			case *discordgo.SelectMenu:
				menu = child
			default:
				continue
			}
			if menu.CustomID == customID {
				values := make([]string, 0, len(menu.Options))
				for _, option := range menu.Options {
					values = append(values, option.Value)
				}
				return values
			}
		}
	}
	t.Fatalf("no select %q in edited message", customID)
	return nil
}

func editCustomIDs(t *testing.T, edit *discordgo.WebhookEdit) []string {
	t.Helper()
	if edit.Components == nil {
		t.Fatal("edited message has no components")
	}
	ids := []string{}
	for _, component := range *edit.Components {
		row, ok := component.(discordgo.ActionsRow)
		if !ok {
			t.Fatalf("panel component is not an action row: %T", component)
		}
		for _, child := range row.Components {
			switch child := child.(type) {
			case discordgo.SelectMenu:
				ids = append(ids, child.CustomID)
			case *discordgo.SelectMenu:
				ids = append(ids, child.CustomID)
			case discordgo.Button:
				ids = append(ids, child.CustomID)
			case *discordgo.Button:
				ids = append(ids, child.CustomID)
			default:
				t.Fatalf("panel child is not a control: %T", child)
			}
		}
	}
	return ids
}

func TestPanelOpensOneRegularMessage(t *testing.T) {
	keys := sdk.NewKeyPool("k1", "k2")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys)
	handler := panelTestHandler(session, keys, []sdk.Model{{ID: "m1"}})
	fake := &fakeInteractionAPI{}
	if err := handler.openPanel(fake, panelCommandInteraction()); err != nil {
		t.Fatal(err)
	}
	if len(fake.responds) != 1 {
		t.Fatalf("expected one opening response: %+v", fake.responds)
	}
	open := fake.responds[0]
	if open.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("panel must open deferred: %+v", open)
	}
	if open.Data != nil && open.Data.Flags&discordgo.MessageFlagsEphemeral != 0 {
		t.Fatalf("panel must not be ephemeral: ephemeral messages cannot be edited")
	}
	if len(fake.edits) != 1 {
		t.Fatalf("panel must render exactly one message: %+v", fake.edits)
	}
	edit := lastEdit(t, fake)
	if rows := len(*edit.Components); rows != 5 {
		t.Fatalf("panel must have 5 rows: %d", rows)
	}
	ids := editCustomIDs(t, edit)
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicated custom ID %q: Discord rejects the message", id)
		}
		seen[id] = true
	}
	for _, want := range []string{modelPanelAgentMain, modelPanelAgentSub, modelPanelProvider, modelPanelModel, modelPanelThinking, modelPanelTemp, modelPanelKey} {
		if !seen[want] {
			t.Fatalf("panel misses control %q: %v", want, ids)
		}
	}
	content := editContent(t, edit)
	for _, want := range []string{"Session Model Settings", "Agent:"} {
		if !strings.Contains(content, want) {
			t.Fatalf("panel content misses %q: %q", want, content)
		}
	}
	// The provider menu stays neutral: the current provider shows in the
	// summary text, never as a preselected option.
	for _, option := range panelProviderMenuOptions(t, edit) {
		if option.Default {
			t.Fatalf("provider must not preselect: %+v", option)
		}
	}
	if len(fake.followups) != 0 {
		t.Fatalf("panel must reuse one message, never send new ones: %+v", fake.followups)
	}
}

func panelProviderMenuOptions(t *testing.T, edit *discordgo.WebhookEdit) []discordgo.SelectMenuOption {
	t.Helper()
	if edit.Components == nil {
		t.Fatal("edited message has no components")
	}
	for _, component := range *edit.Components {
		row, ok := component.(discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, child := range row.Components {
			var menu *discordgo.SelectMenu
			switch child := child.(type) {
			case discordgo.SelectMenu:
				menu = &child
			case *discordgo.SelectMenu:
				menu = child
			default:
				continue
			}
			if menu.CustomID == modelPanelProvider {
				return menu.Options
			}
		}
	}
	t.Fatalf("no provider select in edited message")
	return nil
}

func editTopButtons(t *testing.T, edit *discordgo.WebhookEdit) []discordgo.Button {
	t.Helper()
	if edit.Components == nil || len(*edit.Components) == 0 {
		t.Fatal("edited message has no components")
	}
	row, ok := (*edit.Components)[0].(discordgo.ActionsRow)
	if !ok {
		t.Fatal("first row is not an action row")
	}
	buttons := []discordgo.Button{}
	for _, child := range row.Components {
		button, ok := child.(discordgo.Button)
		if !ok {
			if b, ok := child.(*discordgo.Button); ok {
				button = *b
			} else {
				t.Fatalf("top row child is not a button: %T", child)
			}
		}
		buttons = append(buttons, button)
	}
	return buttons
}

func TestPanelPagesCountOptions(t *testing.T) {
	cases := map[int]int{0: 1, 1: 1, 25: 1, 26: 2, 27: 2, 47: 2, 48: 3, 49: 3, 70: 3, 71: 4}
	for n, want := range cases {
		if got := panelPages(n); got != want {
			t.Fatalf("panelPages(%d) = %d, want %d", n, got, want)
		}
	}
}

func TestPanelOptionPagesFitSelectLimit(t *testing.T) {
	items := make([]discordgo.SelectMenuOption, 0, 71)
	for index := 1; index <= 71; index++ {
		items = append(items, discordgo.SelectMenuOption{Label: "m" + strconv.Itoa(index), Value: "m" + strconv.Itoa(index)})
	}
	pages := panelPages(len(items))
	if pages != 4 {
		t.Fatalf("pages = %d", pages)
	}
	seen := map[string]bool{}
	for page := 0; page < pages; page++ {
		options := pagedOptions(items, page)
		if len(options) > panelMenuOptions {
			t.Fatalf("page %d has %d options", page, len(options))
		}
		hasPrev, hasNext := false, false
		for _, option := range options {
			switch option.Value {
			case panelNavPrev:
				hasPrev = true
			case panelNavNext:
				hasNext = true
			default:
				if seen[option.Value] {
					t.Fatalf("item %q on two pages", option.Value)
				}
				seen[option.Value] = true
			}
		}
		if page == 0 && (hasPrev || !hasNext) {
			t.Fatalf("first page must end with Next only: %+v", options)
		}
		if page == pages-1 && (!hasPrev || hasNext) {
			t.Fatalf("last page must start with Previous only: %+v", options)
		}
		if page > 0 && page < pages-1 && (!hasPrev || !hasNext) {
			t.Fatalf("middle page must have Previous and Next: %+v", options)
		}
	}
	if len(seen) != len(items) {
		t.Fatalf("paged options cover %d of %d items", len(seen), len(items))
	}
	// A short list passes through untouched, without navigation entries.
	short := pagedOptions(items[:25], 0)
	if len(short) != 25 {
		t.Fatalf("short list must pass through: %d", len(short))
	}
}

func TestPanelModelPickAppliesImmediately(t *testing.T) {
	keys := sdk.NewKeyPool("k1")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1", Provider: "B.ai", Model: "m1"}, keys)
	models := []sdk.Model{{ID: "m1"}, {ID: "m2"}}
	handler := panelTestHandler(session, keys, models)
	fake := &fakeInteractionAPI{}
	responds, edits := len(fake.responds), len(fake.edits)
	if err := handler.stepPanel(fake, panelComponentInteraction(modelPanelModel, "m2")); err != nil {
		t.Fatal(err)
	}
	if len(fake.responds) != responds+1 || fake.responds[len(fake.responds)-1].Type != discordgo.InteractionResponseDeferredMessageUpdate {
		t.Fatalf("pick must defer the component first: %+v", fake.responds)
	}
	if len(fake.edits) != edits+1 {
		t.Fatalf("pick must rewrite the same message: %+v", fake.edits)
	}
	if got := session.Config().Model; got != "m2" {
		t.Fatalf("model not applied: %q", got)
	}
	if content := editContent(t, lastEdit(t, fake)); !strings.Contains(content, "m2") {
		t.Fatalf("panel must show the applied model: %q", content)
	}
	if len(fake.followups) != 0 {
		t.Fatalf("panel must reuse one message: %+v", fake.followups)
	}
}

func TestPanelPagerButtonsTurnModelPages(t *testing.T) {
	models := make([]sdk.Model, 0, 30)
	for index := 1; index <= 30; index++ {
		models = append(models, sdk.Model{ID: "model-" + strconv.Itoa(index)})
	}
	keys := sdk.NewKeyPool("k1")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1", Provider: "B.ai", Model: "model-1"}, keys)
	handler := panelTestHandler(session, keys, models)
	fake := &fakeInteractionAPI{}
	// Opening lands on the page holding the current model.
	if err := handler.openPanel(fake, panelCommandInteraction()); err != nil {
		t.Fatal(err)
	}
	buttons := editTopButtons(t, lastEdit(t, fake))
	if len(buttons) != 5 {
		t.Fatalf("top row must hold agent + pager buttons: %+v", buttons)
	}
	if buttons[2].CustomID != modelPanelPagePrev || buttons[4].CustomID != modelPanelPageNext {
		t.Fatalf("pager buttons misplaced: %+v", buttons)
	}
	if !buttons[2].Disabled || buttons[4].Disabled {
		t.Fatalf("first page must disable Prev only: %+v", buttons)
	}
	if buttons[3].Label != "(1/2)" || !buttons[3].Disabled {
		t.Fatalf("page indicator wrong: %+v", buttons[3])
	}
	values := editOptionValues(t, lastEdit(t, fake), modelPanelModel)
	if len(values) != 25 {
		t.Fatalf("first page must offer 25 models: %d", len(values))
	}
	for _, value := range values {
		if value == panelNavNext || value == panelNavPrev {
			t.Fatalf("model menu must not carry nav entries: %v", values)
		}
	}
	// Next turns the page; the indicator and disabled sides follow.
	if err := handler.stepPanel(fake, panelButtonInteraction(modelPanelPageNext)); err != nil {
		t.Fatal(err)
	}
	buttons = editTopButtons(t, lastEdit(t, fake))
	if buttons[3].Label != "(2/2)" || buttons[2].Disabled || !buttons[4].Disabled {
		t.Fatalf("last page buttons wrong: %+v", buttons)
	}
	values = editOptionValues(t, lastEdit(t, fake), modelPanelModel)
	if len(values) != 5 || values[4] != "model-30" {
		t.Fatalf("second page must offer the last models: %v", values)
	}
	// Paging past the end clamps; Back returns.
	if err := handler.stepPanel(fake, panelButtonInteraction(modelPanelPageNext)); err != nil {
		t.Fatal(err)
	}
	if label := editTopButtons(t, lastEdit(t, fake))[3].Label; label != "(2/2)" {
		t.Fatalf("pager must clamp at the end: %q", label)
	}
	if err := handler.stepPanel(fake, panelButtonInteraction(modelPanelPagePrev)); err != nil {
		t.Fatal(err)
	}
	if label := editTopButtons(t, lastEdit(t, fake))[3].Label; label != "(1/2)" {
		t.Fatalf("pager must turn back: %q", label)
	}
	// A pick from the second page applies.
	if err := handler.stepPanel(fake, panelButtonInteraction(modelPanelPageNext)); err != nil {
		t.Fatal(err)
	}
	if err := handler.stepPanel(fake, panelComponentInteraction(modelPanelModel, "model-30")); err != nil {
		t.Fatal(err)
	}
	if got := session.Config().Model; got != "model-30" {
		t.Fatalf("second-page pick not applied: %q", got)
	}
	// An unknown value fails inline and is never stored.
	if err := handler.stepPanel(fake, panelComponentInteraction(modelPanelModel, "nope")); err != nil {
		t.Fatal(err)
	}
	if got := session.Config().Model; got != "model-30" {
		t.Fatalf("bad pick must not apply: %q", got)
	}
	if content := editContent(t, lastEdit(t, fake)); !strings.Contains(content, "not available") {
		t.Fatalf("bad pick must fail inline: %q", content)
	}
}

func TestPanelThinkingAndTempSelects(t *testing.T) {
	keys := sdk.NewKeyPool("k1")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys)
	handler := panelTestHandler(session, keys, []sdk.Model{{ID: "m1"}})
	fake := &fakeInteractionAPI{}
	// Pressing the Thinking button unfolds a select menu without applying.
	if err := handler.stepPanel(fake, panelButtonInteraction(modelPanelThinking)); err != nil {
		t.Fatal(err)
	}
	if got := session.Config().ThinkingLevel; got != "" {
		t.Fatalf("unfolding must not apply: %q", got)
	}
	values := editOptionValues(t, lastEdit(t, fake), modelPanelThinkingMenu)
	if len(values) != 5 {
		t.Fatalf("thinking menu must offer 5 levels: %v", values)
	}
	// Picking applies and folds the row back to buttons.
	if err := handler.stepPanel(fake, panelComponentInteraction(modelPanelThinkingMenu, "high")); err != nil {
		t.Fatal(err)
	}
	if got := session.Config().ThinkingLevel; got != sdk.ThinkingHigh {
		t.Fatalf("thinking = %q", got)
	}
	if content := editContent(t, lastEdit(t, fake)); !strings.Contains(content, "Thinking: `high`") {
		t.Fatalf("panel must show applied thinking: %q", content)
	}
	ids := editCustomIDs(t, lastEdit(t, fake))
	for _, id := range ids {
		if id == modelPanelThinkingMenu {
			t.Fatalf("row must fold back to buttons: %v", ids)
		}
	}
	// Pressing the same button twice folds back without changing anything.
	if err := handler.stepPanel(fake, panelButtonInteraction(modelPanelTemp)); err != nil {
		t.Fatal(err)
	}
	if err := handler.stepPanel(fake, panelButtonInteraction(modelPanelTemp)); err != nil {
		t.Fatal(err)
	}
	if session.Config().Temperature != nil {
		t.Fatalf("folding back must not apply: %+v", session.Config().Temperature)
	}
	// The Temp menu offers default plus 0.0-2.0 with the current preselected.
	if err := handler.stepPanel(fake, panelButtonInteraction(modelPanelTemp)); err != nil {
		t.Fatal(err)
	}
	edit := lastEdit(t, fake)
	values = editOptionValues(t, edit, modelPanelTempMenu)
	if len(values) != 22 || values[0] != "default" || values[1] != "0.0" || values[21] != "2.0" {
		t.Fatalf("temp menu must span default + 0.0-2.0: %v", values)
	}
	if err := handler.stepPanel(fake, panelComponentInteraction(modelPanelTempMenu, "1.5")); err != nil {
		t.Fatal(err)
	}
	if got := session.Config().Temperature; got == nil || *got != 1.5 {
		t.Fatalf("temp not applied: %+v", session.Config().Temperature)
	}
	if content := editContent(t, lastEdit(t, fake)); !strings.Contains(content, "Temperature: `1.5`") {
		t.Fatalf("panel must show the temp: %q", content)
	}
	// Default clears the override; garbage fails inline.
	if err := handler.stepPanel(fake, panelButtonInteraction(modelPanelTemp)); err != nil {
		t.Fatal(err)
	}
	if err := handler.stepPanel(fake, panelComponentInteraction(modelPanelTempMenu, "default")); err != nil {
		t.Fatal(err)
	}
	if session.Config().Temperature != nil {
		t.Fatalf("default must clear temp: %+v", session.Config().Temperature)
	}
	if err := handler.stepPanel(fake, panelButtonInteraction(modelPanelThinking)); err != nil {
		t.Fatal(err)
	}
	if err := handler.stepPanel(fake, panelComponentInteraction(modelPanelThinkingMenu, "ultra")); err != nil {
		t.Fatal(err)
	}
	if got := session.Config().ThinkingLevel; got != sdk.ThinkingHigh {
		t.Fatalf("bad pick must not apply: %q", got)
	}
	if content := editContent(t, lastEdit(t, fake)); !strings.Contains(content, "invalid thinking level") {
		t.Fatalf("bad pick must fail inline: %q", content)
	}
}

func TestPanelProviderSwitchKeepsOrResetsModel(t *testing.T) {
	keys := sdk.NewKeyPool("k1")
	otherKeys := sdk.NewKeyPool("k9")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1", Provider: "B.ai", Model: "shared"}, keys)
	catalogs := map[sdk.ProviderID][]sdk.Model{
		"B.ai": {{ID: "shared"}, {ID: "b-only"}},
		"C.ai": {{ID: "shared"}, {ID: "c-only"}},
		"D.ai": {{ID: "d-only"}},
	}
	handler := &ModelSettingsHandler{
		Providers:    []sdk.ProviderID{"B.ai", "C.ai", "D.ai"},
		ProviderKeys: map[sdk.ProviderID]*sdk.KeyPool{"B.ai": keys, "C.ai": otherKeys, "D.ai": otherKeys},
		Models: func(_ context.Context, provider sdk.ProviderID) ([]sdk.Model, error) {
			return catalogs[provider], nil
		},
		ResolveSession: func(_ context.Context, _ sdk.Input) (*sdk.Session, error) {
			return session, nil
		},
	}
	fake := &fakeInteractionAPI{}
	// Switching to a provider that still carries the model keeps it.
	if err := handler.stepPanel(fake, panelComponentInteraction(modelPanelProvider, "C.ai")); err != nil {
		t.Fatal(err)
	}
	if got := session.Config(); got.Provider != "C.ai" || got.Model != "shared" {
		t.Fatalf("model must be kept: %+v", got)
	}
	// Switching to a provider without it resets to the first catalogue model.
	if err := handler.stepPanel(fake, panelComponentInteraction(modelPanelProvider, "D.ai")); err != nil {
		t.Fatal(err)
	}
	if got := session.Config(); got.Provider != "D.ai" || got.Model != "d-only" {
		t.Fatalf("model must reset: %+v", got)
	}
	if content := editContent(t, lastEdit(t, fake)); !strings.Contains(content, "d-only") {
		t.Fatalf("panel must show the reset model: %q", content)
	}
}

func TestPanelAgentModeButtonsPersist(t *testing.T) {
	keys := sdk.NewKeyPool("k1")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys)
	handler := panelTestHandler(session, keys, []sdk.Model{{ID: "m1"}})
	fake := &fakeInteractionAPI{}
	if err := handler.stepPanel(fake, panelButtonInteraction(modelPanelAgentSub)); err != nil {
		t.Fatal(err)
	}
	if got := session.Config().AgentMode; got != sdk.AgentModeSub {
		t.Fatalf("agent mode = %q", got)
	}
	if content := editContent(t, lastEdit(t, fake)); !strings.Contains(content, "Agent: `sub`") {
		t.Fatalf("panel must show sub mode: %q", content)
	}
	if err := handler.stepPanel(fake, panelButtonInteraction(modelPanelAgentMain)); err != nil {
		t.Fatal(err)
	}
	if got := session.Config().AgentMode; got != sdk.AgentModeMain {
		t.Fatalf("agent mode = %q", got)
	}
}

func TestPanelKeyPoolPickApplies(t *testing.T) {
	keys := sdk.NewKeyPool("k1", "k2", "k3")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys)
	handler := panelTestHandler(session, keys, []sdk.Model{{ID: "m1"}})
	fake := &fakeInteractionAPI{}
	if err := handler.stepPanel(fake, panelComponentInteraction(modelPanelKey, "3")); err != nil {
		t.Fatal(err)
	}
	if got := session.Config().KeyIndex; got != 2 {
		t.Fatalf("key index = %d", got)
	}
}

func TestPanelStaleControlsAnswerExpired(t *testing.T) {
	keys := sdk.NewKeyPool("k1")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys)
	handler := panelTestHandler(session, keys, []sdk.Model{{ID: "m1"}})
	fake := &fakeInteractionAPI{}
	stale := panelComponentInteraction("model:back")
	if err := handler.Handle(fake, stale); err != nil {
		t.Fatal(err)
	}
	if len(fake.responds) != 1 {
		t.Fatalf("stale control must answer: %+v", fake.responds)
	}
	// Foreign components are ignored silently.
	if err := handler.Handle(fake, panelComponentInteraction("other:thing")); err != nil {
		t.Fatal(err)
	}
	if len(fake.responds) != 1 || len(fake.edits) != 0 {
		t.Fatalf("foreign control must be ignored: %+v %+v", fake.responds, fake.edits)
	}
}

func TestModelInCatalog(t *testing.T) {
	models := []sdk.Model{{ID: "m1"}}
	if !modelInCatalog(models, "m1") || modelInCatalog(models, "m2") {
		t.Fatal("membership wrong")
	}
}

func TestSessionIDForChannelUsesMapping(t *testing.T) {
	handler := &ModelSettingsHandler{SessionForChannel: func(channelID string) string {
		if channelID == "c1" {
			return "mapped"
		}
		return ""
	}}
	if got := handler.sessionIDFor("c1"); got != "mapped" {
		t.Fatalf("mapped = %q", got)
	}
	if got := handler.sessionIDFor("c9"); got != "discord:channel:c9" {
		t.Fatalf("fallback = %q", got)
	}
}
