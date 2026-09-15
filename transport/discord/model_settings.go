package discord

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

// Custom IDs for the /model control panel. One regular channel message holds
// every control, so IDs stay short: option paging state lives in the
// process-local page store keyed by channel and user, not in custom IDs
// (CHANGE-012, REQ-028).
const (
	modelPanelAgentMain   = "model:agent:main"
	modelPanelAgentSub    = "model:agent:sub"
	modelPanelPagePrev    = "model:page:prev"
	modelPanelPageNext    = "model:page:next"
	modelPanelProvider    = "model:provider"
	modelPanelModel       = "model:model"
	modelPanelThinking    = "model:thinking"
	modelPanelThinkingMenu = "model:thinking:menu"
	modelPanelTemp        = "model:temp"
	modelPanelTempMenu    = "model:temp:menu"
	modelPanelKey         = "model:key"
)

const (
	// panelPageItems is the option count of the first page when a select
	// menu must paginate. Later pages hold one fewer item to leave room
	// for the Previous option, so every page stays within the 25-option
	// select limit.
	panelPageItems = 24
	// panelMenuOptions caps the options of one select menu (Discord limit).
	panelMenuOptions = 25
)

// panelNavNext and panelNavPrev are select option values for option-level
// paging. They are matched before membership validation, so a real provider
// or model ID never needs to avoid them — but an ID equal to a sentinel
// would page instead of applying, hence the unlikely shape.
const (
	panelNavNext = "__panel_next__"
	panelNavPrev = "__panel_prev__"
)

// panelTemperatures are the temperature stops offered by the Temp selector:
// default plus 0.0-2.0 in 0.1 steps (22 states, one select-safe menu).
func panelTemperatures() []string {
	values := make([]string, 0, 22)
	values = append(values, "default")
	for tenth := 0; tenth <= 20; tenth++ {
		values = append(values, strconv.FormatFloat(float64(tenth)/10, 'f', 1, 64))
	}
	return values
}

// modelPanelPages tracks option paging per invoker plus which value selector
// (thinking or temperature) is currently unfolded. Everything else renders
// from the live session config, because every control applies immediately
// and there is no staged state to keep.
type modelPanelPages struct {
	provider int
	model    int
	selector string
}

// panelSelectorThinking and panelSelectorTemp name the value selector
// unfolded in place of the Thinking/Temp button row.
const (
	panelSelectorThinking = "thinking"
	panelSelectorTemp     = "temp"
)

type ModelSettingsHandler struct {
	ResolveSession    func(context.Context, sdk.Input) (*sdk.Session, error)
	SessionForChannel func(channelID string) string
	Providers         []sdk.ProviderID
	ProviderKeys      map[sdk.ProviderID]*sdk.KeyPool
	Models            func(context.Context, sdk.ProviderID) ([]sdk.Model, error)

	mu    sync.Mutex
	pages map[string]*modelPanelPages
}

func (h *ModelSettingsHandler) sessionIDFor(channelID string) string {
	channelID = strings.TrimSpace(channelID)
	if h != nil && h.SessionForChannel != nil {
		if id := strings.TrimSpace(h.SessionForChannel(channelID)); id != "" {
			return id
		}
	}
	return "discord:channel:" + channelID
}

// panelKey scopes paging state to the invoking user in the invoking channel.
func panelKey(channelID, userID string) string {
	return strings.TrimSpace(channelID) + "\x00" + strings.TrimSpace(userID)
}

func interactionUserID(i *discordgo.InteractionCreate) string {
	if i == nil {
		return ""
	}
	if i.Member != nil && i.Member.User != nil && strings.TrimSpace(i.Member.User.ID) != "" {
		return i.Member.User.ID
	}
	if i.User != nil {
		return strings.TrimSpace(i.User.ID)
	}
	return ""
}

func (h *ModelSettingsHandler) panelPagesFor(channelID, userID string) *modelPanelPages {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pages == nil {
		h.pages = make(map[string]*modelPanelPages)
	}
	key := panelKey(channelID, userID)
	pages, ok := h.pages[key]
	if !ok {
		pages = &modelPanelPages{}
		h.pages[key] = pages
	}
	return pages
}

func (h *ModelSettingsHandler) Handle(s interactionAPI, i *discordgo.InteractionCreate) error {
	if h == nil || i == nil {
		return nil
	}
	if i.Type == discordgo.InteractionApplicationCommand {
		if i.ApplicationCommandData().Name != "model" {
			return nil
		}
		return h.openPanel(s, i)
	}
	if i.Type != discordgo.InteractionMessageComponent {
		return nil
	}
	switch i.MessageComponentData().CustomID {
	case modelPanelAgentMain, modelPanelAgentSub, modelPanelPagePrev, modelPanelPageNext,
		modelPanelProvider, modelPanelModel, modelPanelThinking, modelPanelThinkingMenu,
		modelPanelTemp, modelPanelTempMenu, modelPanelKey:
		return h.stepPanel(s, i)
	default:
		if strings.HasPrefix(i.MessageComponentData().CustomID, "model:") {
			return h.respondError(s, i, "this panel is stale; run /model again")
		}
		return nil
	}
}

// openPanel answers /model with one regular channel message holding every
// control. The catalogue read can exceed the 3s interaction budget, so the
// command defers (non-ephemeral) and edits the same message: the channel
// still ends up with exactly one panel message (REQ-024, REQ-028).
func (h *ModelSettingsHandler) openPanel(s interactionAPI, i *discordgo.InteractionCreate) error {
	if len(h.Providers) == 0 {
		return h.respondError(s, i, "no providers are configured")
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		return err
	}
	pages := h.panelPagesFor(i.ChannelID, interactionUserID(i))
	pages.provider, pages.model = 0, 0
	content, components, err := h.renderPanel(i, pages, "")
	if err != nil {
		return editOriginalMessage(s, i, "Model settings error: "+err.Error(), nil)
	}
	return editOriginalMessage(s, i, content, components)
}

// stepPanel serves every panel control behind a deferred message update,
// applies the pick immediately, then rewrites the same message (REQ-024).
func (h *ModelSettingsHandler) stepPanel(s interactionAPI, i *discordgo.InteractionCreate) error {
	data := i.MessageComponentData()
	userID := interactionUserID(i)
	if userID == "" {
		return h.respondError(s, i, "could not tell who invoked the panel")
	}
	if err := deferMessageUpdate(s, i); err != nil {
		return err
	}
	pages := h.panelPagesFor(i.ChannelID, userID)
	content, components, err := h.applyPick(i, pages, data)
	if err != nil {
		// Re-render the current panel with the failure inline: the channel
		// keeps one message and the next successful pick clears the notice.
		content, components, _ = h.renderPanel(i, pages, err.Error())
	}
	return editOriginalMessage(s, i, content, components)
}

// applyPick applies one control interaction and renders the panel again.
func (h *ModelSettingsHandler) applyPick(i *discordgo.InteractionCreate, pages *modelPanelPages, data discordgo.MessageComponentInteractionData) (string, []discordgo.MessageComponent, error) {
	choice := ""
	if len(data.Values) > 0 {
		choice = strings.TrimSpace(data.Values[0])
	}
	var err error
	switch data.CustomID {
	case modelPanelAgentMain:
		pages.selector = ""
		err = h.pickAgentMode(i, sdk.AgentModeMain)
	case modelPanelAgentSub:
		pages.selector = ""
		err = h.pickAgentMode(i, sdk.AgentModeSub)
	case modelPanelPagePrev:
		pages.selector = ""
		if pages.model > 0 {
			pages.model--
		}
	case modelPanelPageNext:
		pages.selector = ""
		pages.model++
	case modelPanelProvider:
		pages.selector = ""
		err = h.pickProvider(i, pages, choice)
	case modelPanelModel:
		pages.selector = ""
		err = h.pickModel(i, pages, choice)
	case modelPanelThinking:
		h.toggleSelector(pages, panelSelectorThinking)
	case modelPanelThinkingMenu:
		pages.selector = ""
		err = h.pickThinking(i, choice)
	case modelPanelTemp:
		h.toggleSelector(pages, panelSelectorTemp)
	case modelPanelTempMenu:
		pages.selector = ""
		err = h.pickTemperature(i, choice)
	case modelPanelKey:
		pages.selector = ""
		err = h.pickKey(i, choice)
	default:
		err = fmt.Errorf("unknown control")
	}
	if err != nil {
		return "", nil, err
	}
	return h.renderPanel(i, pages, "")
}

func (h *ModelSettingsHandler) resolve(i *discordgo.InteractionCreate) (*sdk.Session, error) {
	if h.ResolveSession == nil {
		return nil, fmt.Errorf("session manager is not configured")
	}
	return h.ResolveSession(context.Background(), sdk.Input{SessionID: h.sessionIDFor(i.ChannelID)})
}

func (h *ModelSettingsHandler) pickAgentMode(i *discordgo.InteractionCreate, mode sdk.AgentMode) error {
	session, err := h.resolve(i)
	if err != nil {
		return err
	}
	return session.SetAgentMode(mode)
}

func (h *ModelSettingsHandler) pickProvider(i *discordgo.InteractionCreate, pages *modelPanelPages, choice string) error {
	switch choice {
	case panelNavNext:
		pages.provider++
		return nil
	case panelNavPrev:
		if pages.provider > 0 {
			pages.provider--
		}
		return nil
	}
	provider := sdk.ProviderID(choice)
	if provider == "" {
		return fmt.Errorf("provider is required")
	}
	if !h.hasProvider(provider) {
		return fmt.Errorf("unknown provider %q", choice)
	}
	keys := h.ProviderKeys[provider]
	if keys == nil {
		return fmt.Errorf("provider %q has no API key pool", provider)
	}
	session, err := h.resolve(i)
	if err != nil {
		return err
	}
	previousModel := session.Config().Model
	// SetProvider clears the model and key index; restore a valid model so
	// the session keeps working after every provider switch (REQ-028).
	if err := session.SetProvider(provider, keys); err != nil {
		return err
	}
	models, err := h.loadModels(provider)
	if err != nil {
		return err
	}
	model := previousModel
	if !modelInCatalog(models, model) {
		model = ""
		if len(models) > 0 {
			model = models[0].ID
		}
	}
	if model == "" {
		return fmt.Errorf("provider %q has no models", provider)
	}
	if err := session.SetModel(model); err != nil {
		return err
	}
	pages.model = modelPageFor(models, model)
	return nil
}

func (h *ModelSettingsHandler) pickModel(i *discordgo.InteractionCreate, pages *modelPanelPages, choice string) error {
	if choice == "" {
		return fmt.Errorf("model is required")
	}
	session, err := h.resolve(i)
	if err != nil {
		return err
	}
	models, err := h.loadModels(session.Config().Provider)
	if err != nil {
		return err
	}
	if !modelInCatalog(models, choice) {
		return fmt.Errorf("model %q is not available for provider %q", choice, session.Config().Provider)
	}
	return session.SetModel(choice)
}

// toggleSelector unfolds a value selector in place of the Thinking/Temp
// button row; pressing the same button again folds it back.
func (h *ModelSettingsHandler) toggleSelector(pages *modelPanelPages, selector string) {
	if pages.selector == selector {
		pages.selector = ""
		return
	}
	pages.selector = selector
}

// pickThinking applies one thinking level from the unfolded selector menu.
func (h *ModelSettingsHandler) pickThinking(i *discordgo.InteractionCreate, choice string) error {
	choice = strings.ToLower(choice)
	if choice == "" || choice == "default" {
		session, err := h.resolve(i)
		if err != nil {
			return err
		}
		return session.ClearThinkingLevel()
	}
	if !validDiscordThinkingLevel(choice) {
		return fmt.Errorf("invalid thinking level %q", choice)
	}
	session, err := h.resolve(i)
	if err != nil {
		return err
	}
	return session.SetThinkingLevel(sdk.ThinkingLevel(choice))
}

// pickTemperature applies one temperature from the unfolded selector menu:
// default clears the override, otherwise a 0.0-2.0 number.
func (h *ModelSettingsHandler) pickTemperature(i *discordgo.InteractionCreate, choice string) error {
	if choice == "" || choice == "default" {
		session, err := h.resolve(i)
		if err != nil {
			return err
		}
		return session.ClearTemperature()
	}
	value, err := strconv.ParseFloat(choice, 64)
	if err != nil || value < 0 || value > 2 {
		return fmt.Errorf("temperature must be a number from 0.0 to 2.0")
	}
	session, err := h.resolve(i)
	if err != nil {
		return err
	}
	return session.SetTemperature(value)
}

func (h *ModelSettingsHandler) pickKey(i *discordgo.InteractionCreate, choice string) error {
	index, err := strconv.Atoi(choice)
	if err != nil || index < 1 {
		return fmt.Errorf("API Pool must be a positive number")
	}
	session, err := h.resolve(i)
	if err != nil {
		return err
	}
	return session.SetKeyIndex(index - 1)
}

func (h *ModelSettingsHandler) loadModels(provider sdk.ProviderID) ([]sdk.Model, error) {
	if h.Models == nil {
		return nil, fmt.Errorf("model catalogue loader is not configured")
	}
	models, err := h.Models(context.Background(), provider)
	if err != nil {
		return nil, err
	}
	kept := models[:0]
	for _, model := range models {
		if strings.TrimSpace(model.ID) != "" {
			kept = append(kept, model)
		}
	}
	return kept, nil
}

// renderPanel builds the one panel message: summary content plus the five
// control rows. failure, when non-empty, is shown inline above the summary
// until the next successful pick clears it.
func (h *ModelSettingsHandler) renderPanel(i *discordgo.InteractionCreate, pages *modelPanelPages, failure string) (string, []discordgo.MessageComponent, error) {
	session, err := h.resolve(i)
	if err != nil {
		return "", nil, err
	}
	config := session.Config()
	provider := config.Provider
	if provider == "" && len(h.Providers) > 0 {
		provider = h.Providers[0]
	}
	models, err := h.loadModels(provider)
	if err != nil {
		return "", nil, err
	}
	if len(models) == 0 {
		return "", nil, fmt.Errorf("provider %q has no models", provider)
	}
	pages.provider = clampPage(panelPages(countProviders(h.Providers)), pages.provider)
	modelPages := modelMenuPages(len(models))
	pages.model = clampPage(modelPages, pages.model)

	content := sessionSettingsSummary(config) + "\nAgent: `" + string(panelAgentMode(config)) + "`"
	if failure != "" {
		content = "Model settings error: " + failure + "\n" + content
	}
	components := []discordgo.MessageComponent{
		panelTopRow(panelAgentMode(config), modelPages, pages.model),
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{CustomID: modelPanelProvider, MenuType: discordgo.StringSelectMenu, Placeholder: "Select provider", Options: pagedProviderOptions(h.Providers, pages.provider), MinValues: intPtr(1), MaxValues: 1},
		}},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{CustomID: modelPanelModel, MenuType: discordgo.StringSelectMenu, Placeholder: "Select model", Options: modelMenuOptions(models, pages.model, config.Model), MinValues: intPtr(1), MaxValues: 1},
		}},
		panelValueRow(pages.selector, config),
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{CustomID: modelPanelKey, MenuType: discordgo.StringSelectMenu, Placeholder: "Select API key pool", Options: makeKeyOptions(panelKeyCount(h, provider), strconv.Itoa(config.KeyIndex+1)), MinValues: intPtr(1), MaxValues: 1},
		}},
	}
	return content, components, nil
}

// panelTopRow holds the agent toggle plus, for multi-page catalogues, the
// pager buttons with a page indicator: everything stays in one row so the
// panel never exceeds five action rows.
func panelTopRow(mode sdk.AgentMode, modelPages, modelPage int) discordgo.ActionsRow {
	mainStyle, subStyle := discordgo.SecondaryButton, discordgo.SecondaryButton
	if mode == sdk.AgentModeSub {
		subStyle = discordgo.PrimaryButton
	} else {
		mainStyle = discordgo.PrimaryButton
	}
	buttons := []discordgo.MessageComponent{
		discordgo.Button{CustomID: modelPanelAgentMain, Style: mainStyle, Label: "Main agent"},
		discordgo.Button{CustomID: modelPanelAgentSub, Style: subStyle, Label: "Sub agent"},
	}
	if modelPages > 1 {
		buttons = append(buttons,
			discordgo.Button{CustomID: modelPanelPagePrev, Style: discordgo.SecondaryButton, Label: "◀ Prev", Disabled: modelPage <= 0},
			discordgo.Button{CustomID: modelPanelPagePrev + ":label", Style: discordgo.SecondaryButton, Label: fmt.Sprintf("(%d/%d)", modelPage+1, modelPages), Disabled: true},
			discordgo.Button{CustomID: modelPanelPageNext, Style: discordgo.SecondaryButton, Label: "Next ▶", Disabled: modelPage >= modelPages-1},
		)
	}
	return discordgo.ActionsRow{Components: buttons}
}

// panelValueRow renders the Thinking/Temp buttons, or the unfolded value
// selector when one of them was just pressed.
func panelValueRow(selector string, config sdk.SessionConfig) discordgo.ActionsRow {
	switch selector {
	case panelSelectorThinking:
		return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{CustomID: modelPanelThinkingMenu, MenuType: discordgo.StringSelectMenu, Placeholder: "Select thinking level", Options: makeThinkingOptions(panelThinkingLabel(config)), MinValues: intPtr(1), MaxValues: 1},
		}}
	case panelSelectorTemp:
		return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{CustomID: modelPanelTempMenu, MenuType: discordgo.StringSelectMenu, Placeholder: "Select temperature", Options: makePanelTempOptions(temperatureLabel(config.Temperature)), MinValues: intPtr(1), MaxValues: 1},
		}}
	default:
		return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{CustomID: modelPanelThinking, Style: discordgo.SecondaryButton, Label: "Thinking: " + panelThinkingLabel(config)},
			discordgo.Button{CustomID: modelPanelTemp, Style: discordgo.SecondaryButton, Label: "Temp: " + temperatureLabel(config.Temperature)},
		}}
	}
}

func makePanelTempOptions(current string) []discordgo.SelectMenuOption {
	options := make([]discordgo.SelectMenuOption, 0, 22)
	for _, value := range panelTemperatures() {
		label := value
		if value == "default" {
			label = "Default"
		}
		options = append(options, discordgo.SelectMenuOption{Label: label, Value: value, Default: value == current})
	}
	return options
}

func panelAgentMode(config sdk.SessionConfig) sdk.AgentMode {
	if config.AgentMode == sdk.AgentModeSub {
		return sdk.AgentModeSub
	}
	return sdk.AgentModeMain
}

func panelThinkingLabel(config sdk.SessionConfig) string {
	if config.ThinkingLevel == "" {
		return "default"
	}
	return string(config.ThinkingLevel)
}

func countProviders(providers []sdk.ProviderID) int {
	count := 0
	for _, provider := range providers {
		if strings.TrimSpace(string(provider)) != "" {
			count++
		}
	}
	return count
}

func panelKeyCount(h *ModelSettingsHandler, provider sdk.ProviderID) int {
	if h != nil {
		if keys := h.ProviderKeys[provider]; keys != nil && keys.Len() > 0 {
			return keys.Len()
		}
	}
	return 1
}

// panelPages counts the option pages for n items: a single page while the
// items fit one menu, otherwise a 24-item first page and 23-item later pages
// so Previous/Next navigation always fits the 25-option limit.
func panelPages(n int) int {
	if n <= panelMenuOptions {
		return 1
	}
	return 2 + (n-panelPageItems-1)/23
}

func clampPage(pages, page int) int {
	if page < 0 {
		return 0
	}
	if page >= pages {
		return pages - 1
	}
	return page
}

// panelWindow returns the item slice for one option page.
func panelWindow(n, page int) (int, int) {
	if panelPages(n) == 1 {
		return 0, n
	}
	page = clampPage(panelPages(n), page)
	if page == 0 {
		return 0, panelPageItems
	}
	start := panelPageItems + 23*(page-1)
	end := start + 23
	if end > n {
		end = n
	}
	return start, end
}

// modelPageFor finds the option page holding the model, so opening the panel
// or switching providers lands on the current model instead of page one.
// Model pages hold a full 25 options; the pager buttons turn them.
func modelPageFor(models []sdk.Model, model string) int {
	for index, candidate := range models {
		if candidate.ID == model {
			return index / panelMenuOptions
		}
	}
	return 0
}

// pagedOptions windows items to one option page with Previous/Next entries.
// Every page, including navigation entries, stays within 25 options and
// every custom ID in the message stays unique (CHANGE-011 lesson).
func pagedOptions(items []discordgo.SelectMenuOption, page int) []discordgo.SelectMenuOption {
	pages := panelPages(len(items))
	page = clampPage(pages, page)
	if pages == 1 {
		return items
	}
	start, end := panelWindow(len(items), page)
	options := make([]discordgo.SelectMenuOption, 0, panelMenuOptions)
	if page > 0 {
		options = append(options, discordgo.SelectMenuOption{Label: "← Previous", Value: panelNavPrev})
	}
	options = append(options, items[start:end]...)
	if page < pages-1 {
		options = append(options, discordgo.SelectMenuOption{Label: "Next →", Value: panelNavNext})
	}
	return options
}

// pagedProviderOptions windows providers to one option page with
// Previous/Next entries. Nothing is preselected: the panel shows the current
// provider in its summary text, and the menu stays neutral (REQ-028).
func pagedProviderOptions(providers []sdk.ProviderID, page int) []discordgo.SelectMenuOption {
	items := make([]discordgo.SelectMenuOption, 0, len(providers))
	for _, provider := range providers {
		value := strings.TrimSpace(string(provider))
		if value == "" {
			continue
		}
		items = append(items, discordgo.SelectMenuOption{Label: value, Value: value})
	}
	return pagedOptions(items, page)
}

// modelMenuOptions windows the catalogue to one full 25-option page. Paging
// rides the pager buttons on the top row, so the menu itself carries no
// navigation entries and the current model stays preselected.
func modelMenuOptions(models []sdk.Model, page int, current string) []discordgo.SelectMenuOption {
	pages := (len(models) + panelMenuOptions - 1) / panelMenuOptions
	if pages < 1 {
		pages = 1
	}
	page = clampPage(pages, page)
	start := page * panelMenuOptions
	end := start + panelMenuOptions
	if end > len(models) {
		end = len(models)
	}
	options := make([]discordgo.SelectMenuOption, 0, end-start)
	for _, model := range models[start:end] {
		if strings.TrimSpace(model.ID) == "" {
			continue
		}
		options = append(options, discordgo.SelectMenuOption{Label: model.ID, Value: model.ID, Default: model.ID == current})
	}
	return options
}

// modelMenuPages counts the 25-option model pages for a catalogue.
func modelMenuPages(n int) int {
	pages := (n + panelMenuOptions - 1) / panelMenuOptions
	if pages < 1 {
		return 1
	}
	return pages
}

func (h *ModelSettingsHandler) hasProvider(provider sdk.ProviderID) bool {
	for _, candidate := range h.Providers {
		if candidate == provider {
			return true
		}
	}
	return false
}

func modelInCatalog(models []sdk.Model, model string) bool {
	for _, candidate := range models {
		if candidate.ID == model {
			return true
		}
	}
	return false
}

func validDiscordThinkingLevel(level string) bool {
	switch sdk.ThinkingLevel(level) {
	case sdk.ThinkingNone, sdk.ThinkingLow, sdk.ThinkingMedium, sdk.ThinkingHigh:
		return true
	default:
		return false
	}
}

func (h *ModelSettingsHandler) respondError(s interactionAPI, i *discordgo.InteractionCreate, message string) error {
	data := &discordgo.InteractionResponseData{Content: "Model settings error: " + message, Flags: discordgo.MessageFlagsEphemeral}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: data})
}

func modalValues(i *discordgo.InteractionCreate) map[string]string { values := make(map[string]string, 6); for _, component := range i.ModalSubmitData().Components { label, ok := component.(*discordgo.Label); if !ok { if valueLabel, valueOK := component.(discordgo.Label); valueOK { label = &valueLabel } else { continue } }; switch child := label.Component.(type) { case *discordgo.SelectMenu: if len(child.Values) > 0 { values[child.CustomID] = child.Values[0] }; case discordgo.SelectMenu: if len(child.Values) > 0 { values[child.CustomID] = child.Values[0] }; case *discordgo.TextInput: values[child.CustomID] = child.Value; case discordgo.TextInput: values[child.CustomID] = child.Value } }; return values }
func modalSelectValues(i *discordgo.InteractionCreate) map[string]string { return modalValues(i) }

// sessionSettingsSummary renders the settings report shared by the /model
// panel and the /new channel summary, so both surfaces always describe the
// same fields (REQ-027).
func sessionSettingsSummary(config sdk.SessionConfig) string {
	thinking := string(config.ThinkingLevel)
	if thinking == "" {
		thinking = "default"
	}
	model := config.Model
	if model == "" {
		model = "not set"
	}
	return fmt.Sprintf("**Session Model Settings**\nProvider: `%s`\nModel: `%s`\nThinking: `%s`\nTemperature: `%s`\nAPI Pool: `%d`", config.Provider, model, thinking, temperatureLabel(config.Temperature), config.KeyIndex+1)
}
func temperatureLabel(value *float64) string {
	if value == nil {
		return "default"
	}
	return strconv.FormatFloat(*value, 'f', 1, 64)
}
func boolPtr(value bool) *bool     { return &value }
func intPtr(value int) *int        { return &value }
func makeKeyOptions(count int, current string) []discordgo.SelectMenuOption {
	if count < 1 {
		count = 1
	}
	if count > 25 {
		count = 25
	}
	currentIndex, _ := strconv.Atoi(current)
	options := make([]discordgo.SelectMenuOption, 0, count)
	for index := 1; index <= count; index++ {
		value := strconv.Itoa(index)
		options = append(options, discordgo.SelectMenuOption{Label: "Pool " + value, Value: value, Default: index == currentIndex})
	}
	if currentIndex < 1 || currentIndex > count {
		options[0].Default = true
	}
	return options
}
func makeThinkingOptions(current string) []discordgo.SelectMenuOption {
	values := []string{"default", string(sdk.ThinkingNone), string(sdk.ThinkingLow), string(sdk.ThinkingMedium), string(sdk.ThinkingHigh)}
	options := make([]discordgo.SelectMenuOption, 0, len(values))
	for _, value := range values {
		label := value
		if value == "default" {
			label = "Default"
		}
		options = append(options, discordgo.SelectMenuOption{Label: label, Value: value, Default: value == current || (current == "" && value == "default")})
	}
	return options
}
func (h *ModelSettingsHandler) summary(session *sdk.Session) string {
	return sessionSettingsSummary(session.Config())
}
