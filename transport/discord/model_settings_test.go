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
	// Both menus stay neutral: current values show in the summary text,
	// never as preselected options, so the (p/n) placeholder stays visible.
	for _, option := range panelProviderMenuOptions(t, edit) {
		if option.Default {
			t.Fatalf("provider must not preselect: %+v", option)
		}
	}
	for _, option := range panelModelMenuOptions(t, edit) {
		if option.Default {
			t.Fatalf("model must not preselect: %+v", option)
		}
	}
	if len(fake.followups) != 0 {
		t.Fatalf("panel must reuse one message, never send new ones: %+v", fake.followups)
	}
}

func panelProviderMenuOptions(t *testing.T, edit *discordgo.WebhookEdit) []discordgo.SelectMenuOption {
	t.Helper()
	return panelMenuOptionsByID(t, edit, modelPanelProvider)
}

func panelModelMenuOptions(t *testing.T, edit *discordgo.WebhookEdit) []discordgo.SelectMenuOption {
	t.Helper()
	return panelMenuOptionsByID(t, edit, modelPanelModel)
}

func panelMenuOptionsByID(t *testing.T, edit *discordgo.WebhookEdit, customID string) []discordgo.SelectMenuOption {
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
				return menu.Options
			}
		}
	}
	t.Fatalf("no select %q in edited message", customID)
	return nil
}

func panelMenuPlaceholder(t *testing.T, edit *discordgo.WebhookEdit, customID string) string {
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
				return menu.Placeholder
			}
		}
	}
	t.Fatalf("no select %q in edited message", customID)
	return ""
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

func TestPanelModelPagesConditionalNav(t *testing.T) {
	models := make([]sdk.Model, 0, 30)
	for index := 1; index <= 30; index++ {
		models = append(models, sdk.Model{ID: "model-" + strconv.Itoa(index)})
	}
	keys := sdk.NewKeyPool("k1")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1", Provider: "B.ai", Model: "model-1"}, keys)
	handler := panelTestHandler(session, keys, models)
	fake := &fakeInteractionAPI{}
	// Opening lands on the page holding the current model. The first page
	// carries only Next plus 24 models, with the page in the menu name.
	if err := handler.openPanel(fake, panelCommandInteraction()); err != nil {
		t.Fatal(err)
	}
	edit := lastEdit(t, fake)
	if placeholder := panelMenuPlaceholder(t, edit, modelPanelModel); placeholder != "🤖 Select model (1/2)" {
		t.Fatalf("placeholder = %q", placeholder)
	}
	options := panelModelMenuOptions(t, edit)
	if len(options) != 25 {
		t.Fatalf("first page must fill the menu: %d", len(options))
	}
	if options[0].Value != "model-1" || options[23].Value != "model-24" || options[24].Value != panelNavNext {
		t.Fatalf("first page must trail with Next only: %+v", options[:2])
	}
	if buttons := editTopButtons(t, edit); len(buttons) != 2 {
		t.Fatalf("top row must be agent buttons only: %+v", buttons)
	}
	// Next turns the page; the pick itself never applies a model. The last
	// page carries only Previous plus the remaining models.
	if err := handler.stepPanel(fake, panelComponentInteraction(modelPanelModel, panelNavNext)); err != nil {
		t.Fatal(err)
	}
	if got := session.Config().Model; got != "model-1" {
		t.Fatalf("navigation must not apply a model: %q", got)
	}
	edit = lastEdit(t, fake)
	if placeholder := panelMenuPlaceholder(t, edit, modelPanelModel); placeholder != "🤖 Select model (2/2)" {
		t.Fatalf("placeholder = %q", placeholder)
	}
	options = panelModelMenuOptions(t, edit)
	if len(options) != 7 || options[0].Value != panelNavPrev || options[6].Value != "model-30" {
		t.Fatalf("second page wrong: %+v", options)
	}
	for _, option := range options {
		if option.Value == panelNavNext {
			t.Fatalf("last page must not offer Next: %+v", options)
		}
	}
	for _, option := range options {
		if option.Value == panelNavNext {
			t.Fatalf("last page must not offer Next: %+v", options)
		}
	}
	// Paging past the edges clamps; Previous returns.
	if err := handler.stepPanel(fake, panelComponentInteraction(modelPanelModel, panelNavNext)); err != nil {
		t.Fatal(err)
	}
	if placeholder := panelMenuPlaceholder(t, lastEdit(t, fake), modelPanelModel); placeholder != "🤖 Select model (2/2)" {
		t.Fatalf("pager must clamp at the end: %q", placeholder)
	}
	if err := handler.stepPanel(fake, panelComponentInteraction(modelPanelModel, panelNavPrev)); err != nil {
		t.Fatal(err)
	}
	if placeholder := panelMenuPlaceholder(t, lastEdit(t, fake), modelPanelModel); placeholder != "🤖 Select model (1/2)" {
		t.Fatalf("pager must turn back: %q", placeholder)
	}
	// The retired v1.61 pager buttons still page.
	if err := handler.stepPanel(fake, panelButtonInteraction(modelPanelPageNext)); err != nil {
		t.Fatal(err)
	}
	if placeholder := panelMenuPlaceholder(t, lastEdit(t, fake), modelPanelModel); placeholder != "🤖 Select model (2/2)" {
		t.Fatalf("old pager button must still work: %q", placeholder)
	}
	// A pick from the second page applies.
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

func TestPanelModelPagesShareProviderScheme(t *testing.T) {
	cases := map[int]int{0: 1, 1: 1, 25: 1, 26: 2, 47: 2, 48: 3, 49: 3, 70: 3, 71: 4}
	for n, want := range cases {
		if got := modelMenuPages(n); got != want {
			t.Fatalf("modelMenuPages(%d) = %d, want %d", n, got, want)
		}
	}
	if got := modelPageFor([]sdk.Model{{ID: "a"}, {ID: "b"}}, "b"); got != 0 {
		t.Fatalf("single page must be 0: %d", got)
	}
}

func panelModalSubmit(thinking, temperature string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      discordgo.InteractionModalSubmit,
		ChannelID: "c1",
		Member:    &discordgo.Member{User: &discordgo.User{ID: "u1"}},
		Data: discordgo.ModalSubmitInteractionData{
			CustomID: modelSettingsModalID,
			Components: []discordgo.MessageComponent{
				discordgo.Label{Label: "Thinking", Component: discordgo.SelectMenu{CustomID: "thinking", Values: []string{thinking}}},
				discordgo.Label{Label: "Temperature", Component: discordgo.TextInput{CustomID: "temperature", Value: temperature}},
			},
		},
	}}
}

func TestPanelThinkingAndTempModal(t *testing.T) {
	keys := sdk.NewKeyPool("k1")
	session := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys)
	handler := panelTestHandler(session, keys, []sdk.Model{{ID: "m1"}})
	fake := &fakeInteractionAPI{}
	// Either button opens the same modal immediately, without deferring.
	for _, button := range []string{modelPanelThinking, modelPanelTemp} {
		responds := len(fake.responds)
		if err := handler.stepPanel(fake, panelButtonInteraction(button)); err != nil {
			t.Fatal(err)
		}
		if len(fake.responds) != responds+1 {
			t.Fatalf("%s must answer once: %+v", button, fake.responds)
		}
		opened := fake.responds[len(fake.responds)-1]
		if opened.Type != discordgo.InteractionResponseModal {
			t.Fatalf("%s must open a modal: %+v", button, opened)
		}
		if opened.Data.CustomID != modelSettingsModalID {
			t.Fatalf("modal ID = %q", opened.Data.CustomID)
		}
		if len(opened.Data.Components) != 2 {
			t.Fatalf("modal must hold thinking + temperature: %+v", opened.Data.Components)
		}
	}
	if len(fake.edits) != 0 || len(fake.followups) != 0 {
		t.Fatalf("opening a modal must not touch messages: %+v %+v", fake.edits, fake.followups)
	}
	if session.Config().ThinkingLevel != "" || session.Config().Temperature != nil {
		t.Fatalf("opening a modal must not apply: %+v", session.Config())
	}
	// Submitting applies both values and rewrites the panel message itself.
	if err := handler.Handle(fake, panelModalSubmit("high", "1.5")); err != nil {
		t.Fatal(err)
	}
	updated := fake.responds[len(fake.responds)-1]
	if updated.Type != discordgo.InteractionResponseUpdateMessage {
		t.Fatalf("submit must update the panel message: %+v", updated)
	}
	if got := session.Config(); got.ThinkingLevel != sdk.ThinkingHigh || got.Temperature == nil || *got.Temperature != 1.5 {
		t.Fatalf("modal values not applied: %+v", got)
	}
	if !strings.Contains(updated.Data.Content, "Thinking: `high`") || !strings.Contains(updated.Data.Content, "Temperature: `1.5`") {
		t.Fatalf("panel not rewritten: %q", updated.Data.Content)
	}
	if len(updated.Data.Components) != 5 {
		t.Fatalf("rewritten panel must keep 5 rows: %+v", updated.Data.Components)
	}
	// Default clears both overrides.
	if err := handler.Handle(fake, panelModalSubmit("default", "default")); err != nil {
		t.Fatal(err)
	}
	if got := session.Config(); got.ThinkingLevel != "" || got.Temperature != nil {
		t.Fatalf("default must clear: %+v", got)
	}
	// Garbage fails ephemeral with the session untouched.
	before := session.Config()
	if err := handler.Handle(fake, panelModalSubmit("high", "abc")); err != nil {
		t.Fatal(err)
	}
	failed := fake.responds[len(fake.responds)-1]
	if failed.Type != discordgo.InteractionResponseChannelMessageWithSource || failed.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("bad temp must fail ephemeral: %+v", failed)
	}
	if !strings.Contains(failed.Data.Content, "temperature") {
		t.Fatalf("bad temp error wrong: %q", failed.Data.Content)
	}
	if session.Config() != before {
		t.Fatalf("bad temp must not apply: %+v", session.Config())
	}
	if err := handler.Handle(fake, panelModalSubmit("ultra", "0.7")); err != nil {
		t.Fatal(err)
	}
	failed = fake.responds[len(fake.responds)-1]
	if !strings.Contains(failed.Data.Content, "thinking") {
		t.Fatalf("bad thinking error wrong: %q", failed.Data.Content)
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
