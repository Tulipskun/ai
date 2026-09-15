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
	modelPanelAgentMain = "model:agent:main"
	modelPanelAgentSub  = "model:agent:sub"
	modelPanelPagePrev  = "model:page:prev"
	modelPanelPageNext  = "model:page:next"
	modelPanelProvider  = "model:provider"
	modelPanelModel     = "model:model"
	modelPanelThinking  = "model:thinking"
	modelPanelTemp      = "model:temp"
	modelPanelKey       = "model:key"
	modelPanelSave      = "model:save"
)

// modelSettingsModalID is the modal opened by the Thinking/Temp buttons. It
// carries a thinking select menu plus a temperature text field; submitting
// applies both and rewrites the panel message (CHANGE-014).
const modelSettingsModalID = "model:settings"

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

// modelPanelPages tracks option paging per invoker. Everything else renders
// from the live session config, because every control applies immediately
// and there is no staged state to keep.
type modelPanelPages struct {
	provider int
	model    int
}

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
		if i.Type == discordgo.InteractionModalSubmit && i.ModalSubmitData().CustomID == modelSettingsModalID {
			return h.submitModal(s, i)
		}
		return nil
	}
	// The v1.61 pager buttons are gone from the panel, but their handler
	// stays so panels opened before the upgrade keep paging (CHANGE-014).
	switch i.MessageComponentData().CustomID {
	case modelPanelAgentMain, modelPanelAgentSub, modelPanelPagePrev, modelPanelPageNext,
		modelPanelProvider, modelPanelModel, modelPanelThinking,
		modelPanelTemp, modelPanelKey, modelPanelSave:
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
	return editPanelMessage(s, i, content, components)
}

// stepPanel serves every panel control: the Thinking/Temp buttons open the
// settings modal immediately (a modal cannot follow a deferred response),
// everything else applies behind a deferred message update and rewrites the
// same message (REQ-024).
func (h *ModelSettingsHandler) stepPanel(s interactionAPI, i *discordgo.InteractionCreate) error {
	data := i.MessageComponentData()
	userID := interactionUserID(i)
	if userID == "" {
		return h.respondError(s, i, "could not tell who invoked the panel")
	}
	if data.CustomID == modelPanelThinking || data.CustomID == modelPanelTemp {
		return h.openModal(s, i)
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
	return editPanelMessage(s, i, content, components)
}

// editPanelMessage rewrites the panel message as Components V2, keeping one
// message updated instead of sending new ones. The V2 flag rides every edit
// so the message keeps its layout.
func editPanelMessage(s interactionAPI, i *discordgo.InteractionCreate, content string, components []discordgo.MessageComponent) error {
	_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content:    &content,
		Components: &components,
		Flags:      discordgo.MessageFlagsIsComponentsV2,
	})
	return err
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
		err = h.pickAgentMode(i, pages, sdk.AgentModeMain)
	case modelPanelAgentSub:
		err = h.pickAgentMode(i, pages, sdk.AgentModeSub)
	case modelPanelPagePrev:
		if pages.model > 0 {
			pages.model--
		}
	case modelPanelPageNext:
		pages.model++
	case modelPanelProvider:
		err = h.pickProvider(i, pages, choice)
	case modelPanelModel:
		err = h.pickModel(i, pages, choice)
	case modelPanelKey:
		err = h.pickKey(i, choice)
	case modelPanelSave:
		return h.frozenPanel(i)
	default:
		err = fmt.Errorf("unknown control")
	}
	if err != nil {
		return "", nil, err
	}
	return h.renderPanel(i, pages, "")
}

// frozenPanel renders the saved panel: the summary container alone, with no
// controls left to press (CHANGE-017).
func (h *ModelSettingsHandler) frozenPanel(i *discordgo.InteractionCreate) (string, []discordgo.MessageComponent, error) {
	session, err := h.resolve(i)
	if err != nil {
		return "", nil, err
	}
	config := session.Config()
	return "", []discordgo.MessageComponent{panelSummaryContainer(config, session.EffectiveConfig(), false)}, nil
}

func (h *ModelSettingsHandler) resolve(i *discordgo.InteractionCreate) (*sdk.Session, error) {
	if h.ResolveSession == nil {
		return nil, fmt.Errorf("session manager is not configured")
	}
	return h.ResolveSession(context.Background(), sdk.Input{SessionID: h.sessionIDFor(i.ChannelID)})
}

func (h *ModelSettingsHandler) pickAgentMode(i *discordgo.InteractionCreate, pages *modelPanelPages, mode sdk.AgentMode) error {
	session, err := h.resolve(i)
	if err != nil {
		return err
	}
	if err := session.SetAgentMode(mode); err != nil {
		return err
	}
	if mode == sdk.AgentModeSub {
		// First entry seeds the sub side from main so both sides start
		// equal and the user diverges from there (REQ-030).
		mainProvider := session.Config().Provider
		if err := session.EnsureSubSettings(h.ProviderKeys[mainProvider]); err != nil {
			return err
		}
	}
	// Land on the active side's model page.
	effective := session.EffectiveConfig()
	if models, err := h.loadModels(effective.Provider); err == nil && len(models) > 0 {
		pages.model = modelPageFor(models, effective.Model)
	}
	return nil
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
	previousModel := session.EffectiveConfig().Model
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
	switch choice {
	case panelNavNext:
		pages.model++
		return nil
	case panelNavPrev:
		if pages.model > 0 {
			pages.model--
		}
		return nil
	}
	if choice == "" {
		return fmt.Errorf("model is required")
	}
	session, err := h.resolve(i)
	if err != nil {
		return err
	}
	effective := session.EffectiveConfig()
	models, err := h.loadModels(effective.Provider)
	if err != nil {
		return err
	}
	if !modelInCatalog(models, choice) {
		return fmt.Errorf("model %q is not available for provider %q", choice, effective.Provider)
	}
	return session.SetModel(choice)
}

// openModal opens the Thinking/Temperature settings modal prefilled with
// the session values. A modal must be the immediate interaction response,
// so this runs before any defer (REQ-024).
func (h *ModelSettingsHandler) openModal(s interactionAPI, i *discordgo.InteractionCreate) error {
	session, err := h.resolve(i)
	if err != nil {
		return h.respondError(s, i, err.Error())
	}
	config := session.EffectiveConfig()
	thinking := string(config.ThinkingLevel)
	if thinking == "" {
		thinking = "default"
	}
	temperature := ""
	if config.Temperature != nil {
		temperature = temperatureLabel(config.Temperature)
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type:     discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: modelSettingsModalID,
			Title:    "Thinking & Temperature",
			Components: []discordgo.MessageComponent{
				discordgo.Label{Label: "Thinking", Description: "Thinking level", Component: discordgo.SelectMenu{CustomID: "thinking", MenuType: discordgo.StringSelectMenu, Placeholder: "Select thinking level", Options: makeThinkingOptions(thinking)}},
				discordgo.Label{Label: "Temperature", Description: "default or 0.0-2.0", Component: discordgo.TextInput{CustomID: "temperature", Style: discordgo.TextInputShort, Placeholder: "default", Value: temperature, Required: boolPtr(false), MaxLength: 8}},
			},
		},
	})
}

// submitModal applies the modal values and rewrites the panel message the
// modal came from via an Update response, so the channel keeps one message.
// The catalogue behind the render is cache-backed, keeping the submit inside
// the interaction budget (CHANGE-014).
func (h *ModelSettingsHandler) submitModal(s interactionAPI, i *discordgo.InteractionCreate) error {
	values := modalValues(i)
	session, err := h.resolve(i)
	if err != nil {
		return h.respondError(s, i, err.Error())
	}
	thinking := strings.ToLower(strings.TrimSpace(values["thinking"]))
	if thinking != "" && thinking != "default" && !validDiscordThinkingLevel(thinking) {
		return h.respondError(s, i, fmt.Sprintf("invalid thinking level %q", values["thinking"]))
	}
	temperatureText := strings.TrimSpace(values["temperature"])
	var temperature *float64
	if temperatureText != "" && temperatureText != "default" {
		value, parseErr := strconv.ParseFloat(temperatureText, 64)
		if parseErr != nil || value < 0 || value > 2 {
			return h.respondError(s, i, "temperature must be default or a number from 0.0 to 2.0")
		}
		temperature = &value
	}
	// Both values validated before anything applies: a bad temperature
	// never leaves a half-applied thinking level behind.
	if thinking == "" || thinking == "default" {
		if err := session.ClearThinkingLevel(); err != nil {
			return h.respondError(s, i, err.Error())
		}
	} else if err := session.SetThinkingLevel(sdk.ThinkingLevel(thinking)); err != nil {
		return h.respondError(s, i, err.Error())
	}
	if temperature == nil {
		if err := session.ClearTemperature(); err != nil {
			return h.respondError(s, i, err.Error())
		}
	} else if err := session.SetTemperature(*temperature); err != nil {
		return h.respondError(s, i, err.Error())
	}
	pages := h.panelPagesFor(i.ChannelID, interactionUserID(i))
	content, components, err := h.renderPanel(i, pages, "")
	if err != nil {
		return h.respondError(s, i, err.Error())
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Content: content, Components: components, Flags: discordgo.MessageFlagsIsComponentsV2},
	})
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

// renderPanel builds the one V2 panel message following the approved sketch:
// a summary container, the control rows, a separator, then Save at the very
// bottom. failure, when non-empty, is shown as message text above the
// container until the next successful pick clears it. Every control reads
// the active side; the summary shows both sides (REQ-028, CHANGE-018).
func (h *ModelSettingsHandler) renderPanel(i *discordgo.InteractionCreate, pages *modelPanelPages, failure string) (string, []discordgo.MessageComponent, error) {
	session, err := h.resolve(i)
	if err != nil {
		return "", nil, err
	}
	config := session.Config()
	effective := session.EffectiveConfig()
	provider := effective.Provider
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

	content := ""
	if failure != "" {
		content = "Model settings error: " + failure
	}
	thinking := string(effective.ThinkingLevel)
	if thinking == "" {
		thinking = "default"
	}
	modelOptions, modelPlaceholder := modelMenuOptions(models, pages.model)
	components := []discordgo.MessageComponent{
		panelSummaryContainer(config, effective, true),
		discordgo.Separator{Divider: boolPtr(true)},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{CustomID: modelPanelProvider, MenuType: discordgo.StringSelectMenu, Placeholder: "📦 Select provider", Options: pagedProviderOptions(h.Providers, pages.provider), MinValues: intPtr(1), MaxValues: 1},
		}},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{CustomID: modelPanelModel, MenuType: discordgo.StringSelectMenu, Placeholder: modelPlaceholder, Options: modelOptions, MinValues: intPtr(1), MaxValues: 1},
		}},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{CustomID: modelPanelKey, MenuType: discordgo.StringSelectMenu, Placeholder: "🔑 Select API key pool", Options: makeKeyOptions(panelKeyCount(h, provider)), MinValues: intPtr(1), MaxValues: 1},
		}},
	}
	return content, components, nil
}

// panelAgentButtonStyle highlights the active side's toggle button.
func panelAgentButtonStyle(config sdk.SessionConfig, mode sdk.AgentMode) discordgo.ButtonStyle {
	if panelAgentMode(config) == mode {
		return discordgo.PrimaryButton
	}
	return discordgo.SecondaryButton
}

// panelSummaryContainer renders the live session settings as a V2 container
// under one accent bar: a title, the main block, a separator, the sub block,
// then every button as a section (text left, button right). Select menus
// cannot live in a container, so they stay as top-level rows outside.
// Summaries are plain text; emoji lives only in buttons and menus
// (REQ-028, CHANGE-019).
func panelSummaryContainer(config, effective sdk.SessionConfig, live bool) discordgo.Container {
	children := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: "**Session Model Settings**"},
		discordgo.TextDisplay{Content: panelModeBlock("**Main agent**", config.Provider, config.Model, config.ThinkingLevel, config.Temperature, config.KeyIndex)},
		discordgo.Separator{Divider: boolPtr(true)},
		discordgo.TextDisplay{Content: panelModeBlock("**Sub agent**", config.Sub.Provider, config.Sub.Model, config.Sub.ThinkingLevel, config.Sub.Temperature, config.Sub.KeyIndex)},
	}
	if !live {
		children = append(children, discordgo.TextDisplay{Content: panelModeStatus(config)})
		return panelContainer(children)
	}
	thinking := string(effective.ThinkingLevel)
	if thinking == "" {
		thinking = "default"
	}
	children = append(children,
		discordgo.Separator{Divider: boolPtr(true)},
		panelButtonSection(modelPanelAgentMain, "🤖 Main agent", panelAgentButtonStyle(config, sdk.AgentModeMain), "**Main agent**\nPlans first, then delegates work to a sub-agent"),
		panelButtonSection(modelPanelAgentSub, "⚡ Sub agent", panelAgentButtonStyle(config, sdk.AgentModeSub), "**Sub agent**\nExecutes directly with the full tool set"),
		panelButtonSection(modelPanelThinking, "💭 Thinking", discordgo.SecondaryButton, "Thinking: `"+thinking+"`"),
		panelButtonSection(modelPanelTemp, "🌡️ Temp", discordgo.SecondaryButton, "Temperature: `"+temperatureLabel(effective.Temperature)+"`"),
		panelButtonSection(modelPanelSave, "💾 Save", discordgo.SuccessButton, "Save these settings and freeze the panel"),
	)
	return panelContainer(children)
}

func panelContainer(children []discordgo.MessageComponent) discordgo.Container {
	accent := 0x5865F2
	return discordgo.Container{AccentColor: &accent, Components: children}
}

// panelButtonSection renders one button inside the container: its label on
// the left, the button on the right. This is the only way buttons can share
// the container's accent bar.
func panelButtonSection(customID, label string, style discordgo.ButtonStyle, text string) discordgo.Section {
	return discordgo.Section{
		Components: []discordgo.MessageComponent{discordgo.TextDisplay{Content: text}},
		Accessory:  discordgo.Button{CustomID: customID, Style: style, Label: label},
	}
}

// panelModeBlock renders one agent side's five settings lines as plain text;
// emoji lives only in buttons and menus (CHANGE-019).
func panelModeBlock(header string, provider sdk.ProviderID, model string, thinking sdk.ThinkingLevel, temperature *float64, pool int) string {
	if provider == "" {
		provider = "not set"
	}
	if model == "" {
		model = "not set"
	}
	level := string(thinking)
	if level == "" {
		level = "default"
	}
	return fmt.Sprintf("%s\nProvider: `%s`\nModel: `%s`\nThinking: `%s`\nTemp: `%s`\nAPI Pool: `%d`",
		header, provider, model, level, temperatureLabel(temperature), pool+1)
}

// panelModeStatus renders the frozen mode line once Save strips the buttons.
func panelModeStatus(config sdk.SessionConfig) string {
	if panelAgentMode(config) == sdk.AgentModeSub {
		return "Mode: Sub"
	}
	return "Mode: Main"
}

func panelAgentMode(config sdk.SessionConfig) sdk.AgentMode {
	if config.AgentMode == sdk.AgentModeSub {
		return sdk.AgentModeSub
	}
	return sdk.AgentModeMain
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
// Model paging shares the provider scheme: a 24-item first page, then
// 23-item later pages (CHANGE-015).
func modelPageFor(models []sdk.Model, model string) int {
	for index, candidate := range models {
		if candidate.ID == model {
			if len(models) <= panelMenuOptions {
				return 0
			}
			if index < panelPageItems {
				return 0
			}
			return 1 + (index-panelPageItems)/23
		}
	}
	return 0
}

// pagedOptions windows items to one option page with Previous/Next entries.
// Next always leads: the first page is Next plus 24 items, middle pages are
// Previous plus Next plus 23 items, the last page is Previous plus the rest.
// Every page stays within 25 options and every custom ID in the message
// stays unique (CHANGE-011 lesson).
func pagedOptions(items []discordgo.SelectMenuOption, page int) []discordgo.SelectMenuOption {
	pages := panelPages(len(items))
	page = clampPage(pages, page)
	if pages == 1 {
		return items
	}
	start, end := panelWindow(len(items), page)
	options := make([]discordgo.SelectMenuOption, 0, panelMenuOptions)
	if page < pages-1 {
		options = append(options, discordgo.SelectMenuOption{Label: "Next →", Value: panelNavNext})
	}
	if page > 0 {
		options = append(options, discordgo.SelectMenuOption{Label: "← Previous", Value: panelNavPrev})
	}
	return append(options, items[start:end]...)
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

// modelMenuOptions windows the catalogue to one option page with the
// provider paging scheme: a single page while it fits, otherwise a 24-item
// first page plus Next, 23-item middle pages plus both, and a Previous-led
// last page. Nothing is preselected: the current model shows in the summary
// text. The menu placeholder names the page, e.g. Select model (1/2).
func modelMenuOptions(models []sdk.Model, page int) ([]discordgo.SelectMenuOption, string) {
	items := make([]discordgo.SelectMenuOption, 0, len(models))
	for _, model := range models {
		if strings.TrimSpace(model.ID) == "" {
			continue
		}
		items = append(items, discordgo.SelectMenuOption{Label: model.ID, Value: model.ID})
	}
	pages := panelPages(len(items))
	page = clampPage(pages, page)
	if pages == 1 {
		return items, "🤖 Select model"
	}
	return pagedOptions(items, page), fmt.Sprintf("🤖 Select model (%d/%d)", page+1, pages)
}

// modelMenuPages counts the model option pages, sharing the provider paging
// scheme (CHANGE-015).
func modelMenuPages(n int) int {
	return panelPages(n)
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
// fallback paths and the /new channel summary: the main block followed by
// the sub block, mirroring the panel (REQ-027, REQ-030).
func sessionSettingsSummary(config sdk.SessionConfig) string {
	return "**Session Model Settings**\n" +
		panelModeBlock("**Main agent**", config.Provider, config.Model, config.ThinkingLevel, config.Temperature, config.KeyIndex) + "\n" +
		panelModeBlock("**Sub agent**", config.Sub.Provider, config.Sub.Model, config.Sub.ThinkingLevel, config.Sub.Temperature, config.Sub.KeyIndex)
}
func temperatureLabel(value *float64) string {
	if value == nil {
		return "default"
	}
	return strconv.FormatFloat(*value, 'f', 1, 64)
}
func boolPtr(value bool) *bool     { return &value }
func intPtr(value int) *int        { return &value }
func makeKeyOptions(count int) []discordgo.SelectMenuOption {
	if count < 1 {
		count = 1
	}
	if count > 25 {
		count = 25
	}
	options := make([]discordgo.SelectMenuOption, 0, count)
	for index := 1; index <= count; index++ {
		value := strconv.Itoa(index)
		options = append(options, discordgo.SelectMenuOption{Label: "Pool " + value, Value: value})
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
