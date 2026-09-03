package discord

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

const (
	modelSettingsPrefix       = "model:settings:"
	modelSettingsProviderStep = "model:provider:"
	modelSettingsProviderSelect = "model:provider:select"
	modelSettingsModelSelectPrefix = "model:select:"
)

type ModelSettingsHandler struct {
	ResolveSession func(context.Context, sdk.Input) (*sdk.Session, error)
	Providers      []sdk.ProviderID
	ProviderKeys   map[sdk.ProviderID]*sdk.KeyPool
	Models         func(context.Context, sdk.ProviderID) ([]sdk.Model, error)
}

func (h *ModelSettingsHandler) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if h == nil || i == nil {
		return nil
	}
	if i.Type == discordgo.InteractionApplicationCommand {
		if i.ApplicationCommandData().Name != "model" {
			return nil
		}
		return h.respondProviderStep(s, i, i.ChannelID)
	}
	if i.Type == discordgo.InteractionMessageComponent {
		customID := i.MessageComponentData().CustomID
		if customID == modelSettingsProviderSelect {
			return h.handleProviderSelect(s, i)
		}
		if strings.HasPrefix(customID, modelSettingsModelSelectPrefix) {
			return h.handleModelSelect(s, i, customID)
		}
		return nil
	}
	if i.Type == discordgo.InteractionModalSubmit {
		customID := i.ModalSubmitData().CustomID
		if strings.HasPrefix(customID, modelSettingsPrefix) {
			return h.handleSettingsSubmit(s, i, customID)
		}
	}
	return nil
}

func (h *ModelSettingsHandler) respondProviderStep(s *discordgo.Session, i *discordgo.InteractionCreate, channelID string) error {
	if h.ResolveSession == nil {
		return h.respondError(s, i, "session manager is not configured")
	}
	if len(h.Providers) == 0 {
		return h.respondError(s, i, "no providers are configured")
	}
	session, err := h.ResolveSession(context.Background(), sdk.Input{SessionID: "discord:channel:" + channelID})
	if err != nil {
		return h.respondError(s, i, err.Error())
	}
	provider := string(session.Config().Provider)
	if provider == "" || !h.hasProvider(sdk.ProviderID(provider)) {
		provider = string(h.Providers[0])
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "Select a provider:",
			Flags: discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.SelectMenu{CustomID: modelSettingsProviderSelect, MenuType: discordgo.StringSelectMenu, Placeholder: "Select provider", Options: makeProviderOptions(h.Providers, provider), MinValues: intPtr(1), MaxValues: 1},
				}},
			},
		},
	})
}

func providerSelectionModal(channelID, current string, providers []sdk.ProviderID) *discordgo.InteractionResponseData {
	return &discordgo.InteractionResponseData{
		CustomID: modelSettingsProviderStep + channelID,
		Title:    "Model Settings — Step 1",
		Components: []discordgo.MessageComponent{
			discordgo.Label{Label: "Provider", Description: "Choose the provider first.", Component: discordgo.SelectMenu{CustomID: "provider", MenuType: discordgo.StringSelectMenu, Placeholder: "Select provider", Options: makeProviderOptions(providers, current), Required: boolPtr(true)}},
	},
	}
}

func providerSelectionMessage(channelID, current string, providers []sdk.ProviderID) *discordgo.InteractionResponseData {
	return &discordgo.InteractionResponseData{
		CustomID: modelSettingsProviderStep + channelID,
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.SelectMenu{CustomID: modelSettingsProviderSelect, MenuType: discordgo.StringSelectMenu, Placeholder: "Select provider", Options: makeProviderOptions(providers, current), MinValues: intPtr(1), MaxValues: 1},
			}},
		},
	}
}

func (h *ModelSettingsHandler) handleProviderSelect(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	values := i.MessageComponentData().Values
	if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return h.respondError(s, i, "provider is required")
	}
	provider := sdk.ProviderID(strings.TrimSpace(values[0]))
	if !h.hasProvider(provider) {
		return h.respondError(s, i, fmt.Sprintf("unknown provider %q", provider))
	}
	if h.ProviderKeys[provider] == nil {
		return h.respondError(s, i, fmt.Sprintf("provider %q has no API key pool", provider))
	}
	if h.Models == nil {
		return h.respondError(s, i, "model catalogue loader is not configured")
	}
	models, err := h.Models(context.Background(), provider)
	if err != nil {
		return h.respondError(s, i, err.Error())
	}
	if len(models) == 0 {
		return h.respondError(s, i, fmt.Sprintf("provider %q has no models", provider))
	}
	groups := makeModelOptionGroups(models, "")
	if len(groups) == 0 {
		return h.respondError(s, i, fmt.Sprintf("provider %q has no usable models", provider))
	}
	components := makeModelSelectionComponents(i.ChannelID, string(provider), groups)
	if len(components) > 5 {
		components = components[:5]
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: fmt.Sprintf("Select a model for `%s`:", provider), Flags: discordgo.MessageFlagsEphemeral, Components: components},
	}); err != nil {
		return err
	}
	for start := 5; start < len(groups); start += 5 {
		end := start + 5
		if end > len(groups) {
			end = len(groups)
		}
		_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content: "More models:",
			Flags: discordgo.MessageFlagsEphemeral,
			Components: makeModelSelectionComponents(i.ChannelID, string(provider), groups[start:end]),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (h *ModelSettingsHandler) handleModelSelect(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) error {
	value := strings.TrimPrefix(customID, modelSettingsModelSelectPrefix)
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return h.respondError(s, i, "invalid model selection")
	}
	channelID, providerText := parts[0], parts[1]
	provider := sdk.ProviderID(providerText)
	if !h.hasProvider(provider) {
		return h.respondError(s, i, fmt.Sprintf("unknown provider %q", provider))
	}
	values := i.MessageComponentData().Values
	if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return h.respondError(s, i, "model is required")
	}
	model := strings.TrimSpace(values[0])
	if h.ResolveSession == nil {
		return h.respondError(s, i, "session manager is not configured")
	}
	session, err := h.ResolveSession(context.Background(), sdk.Input{SessionID: "discord:channel:" + channelID})
	if err != nil {
		return h.respondError(s, i, err.Error())
	}
	if err := session.SetProvider(provider, h.ProviderKeys[provider]); err != nil {
		return h.respondError(s, i, err.Error())
	}
	if err := session.SetModel(model); err != nil {
		return h.respondError(s, i, err.Error())
	}
	config := session.Config()
	keyCount := 1
	if keys := h.ProviderKeys[provider]; keys != nil && keys.Len() > 0 {
		keyCount = keys.Len()
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: modelSettingsModal(session.ID(), string(provider), model, temperatureLabel(config.Temperature), string(config.ThinkingLevel), strconv.Itoa(config.KeyIndex+1), keyCount),
	})
}

func modelSettingsModal(sessionID, provider, model, temperature, thinking, key string, keyCount ...int) *discordgo.InteractionResponseData {
	resolvedKeyCount := 1
	if len(keyCount) > 0 && keyCount[0] > 0 {
		resolvedKeyCount = keyCount[0]
	}
	channelID := strings.TrimPrefix(sessionID, "discord:channel:")
	return &discordgo.InteractionResponseData{
		CustomID: modelSettingsPrefix + channelID + ":" + provider,
		Title:    "Model Settings — Step 2",
		Components: []discordgo.MessageComponent{
			discordgo.Label{Label: "Model", Description: "Selected model: " + model, Component: discordgo.TextInput{CustomID: "model", Style: discordgo.TextInputShort, Value: model, Required: boolPtr(true), MaxLength: 100}},
			discordgo.Label{Label: "Temperature", Description: "Enter a number from 0.0 to 1.0.", Component: discordgo.TextInput{CustomID: "temperature", Style: discordgo.TextInputShort, Value: normalizeTemperatureInput(temperature), Placeholder: "0.0 - 1.0 (blank = default)", Required: boolPtr(false), MaxLength: 20}},
		discordgo.Label{Label: "Thinking", Component: discordgo.SelectMenu{CustomID: "thinking", MenuType: discordgo.StringSelectMenu, Placeholder: "Select thinking level", Options: makeThinkingOptions(thinking), Required: boolPtr(true)}},
		discordgo.Label{Label: "API Pool", Component: discordgo.SelectMenu{CustomID: "key", MenuType: discordgo.StringSelectMenu, Placeholder: "Select API key pool", Options: makeKeyOptions(resolvedKeyCount, key), Required: boolPtr(true)}},
		},
	}
}

func modelSettingsModalWithCatalog(sessionID, provider, model, temperature, thinking, key string, models []sdk.Model, keyCount int) *discordgo.InteractionResponseData {
	return modelSettingsModal(sessionID, provider, model, temperature, thinking, key, keyCount)
}

func makeProviderOptions(providers []sdk.ProviderID, current string) []discordgo.SelectMenuOption {
	options := make([]discordgo.SelectMenuOption, 0, minInt(len(providers), 25))
	for _, provider := range providers {
		value := string(provider)
		if value == "" || len(options) >= 25 {
			continue
		}
		options = append(options, discordgo.SelectMenuOption{Label: value, Value: value, Default: value == current})
	}
	return options
}

func makeModelOptions(models []sdk.Model, current string) []discordgo.SelectMenuOption {
	groups := makeModelOptionGroups(models, current)
	if len(groups) == 0 {
		return nil
	}
	return groups[0]
}

func makeModelOptionGroups(models []sdk.Model, current string) [][]discordgo.SelectMenuOption {
	all := make([]discordgo.SelectMenuOption, 0, len(models))
	for _, model := range models {
		if model.ID == "" {
			continue
		}
		all = append(all, discordgo.SelectMenuOption{Label: model.ID, Value: model.ID, Default: model.ID == current})
	}
	groups := make([][]discordgo.SelectMenuOption, 0, (len(all)+24)/25)
	for start := 0; start < len(all); start += 25 {
		end := start + 25
		if end > len(all) {
			end = len(all)
		}
		group := make([]discordgo.SelectMenuOption, end-start)
		copy(group, all[start:end])
		groups = append(groups, group)
	}
	return groups
}

func makeModelSelectionComponents(channelID, provider string, groups [][]discordgo.SelectMenuOption) []discordgo.MessageComponent {
	components := make([]discordgo.MessageComponent, 0, minInt(len(groups), 5))
	for index, options := range groups {
		start := index*25 + 1
		end := start + len(options) - 1
		customID := modelSettingsModelSelectPrefix + channelID + ":" + provider
		label := fmt.Sprintf("Models %d-%d", start, end)
		components = append(components, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{CustomID: customID, MenuType: discordgo.StringSelectMenu, Placeholder: label, Options: options, MinValues: intPtr(1), MaxValues: 1},
		}})
	}
	return components
}

func makeTemperatureOptions(current string) []discordgo.SelectMenuOption {
	return []discordgo.SelectMenuOption{{Label: current, Value: current, Default: true}}
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

func (h *ModelSettingsHandler) handleSettingsSubmit(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) error {
	value := strings.TrimPrefix(customID, modelSettingsPrefix)
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || h.ResolveSession == nil {
		return h.respondError(s, i, "invalid model settings session")
	}
	channelID := parts[0]
	provider := sdk.ProviderID(parts[1])
	if !h.hasProvider(provider) {
		return h.respondError(s, i, fmt.Sprintf("unknown provider %q", provider))
	}
	session, err := h.ResolveSession(context.Background(), sdk.Input{SessionID: "discord:channel:" + channelID})
	if err != nil {
		return h.respondError(s, i, err.Error())
	}

	values := modalValues(i)
	model := strings.TrimSpace(values["model"])
	thinking := strings.TrimSpace(strings.ToLower(values["thinking"]))
	temperatureText := strings.TrimSpace(values["temperature"])
	keyText := strings.TrimSpace(values["key"])

	keys := h.ProviderKeys[provider]
	if keys == nil {
		return h.respondError(s, i, fmt.Sprintf("provider %q has no API key pool", provider))
	}
	if model == "" {
		return h.respondError(s, i, "model is required")
	}

	var temperature *float64
	if temperatureText != "" && temperatureText != "default" {
		value, parseErr := strconv.ParseFloat(temperatureText, 64)
		if parseErr != nil || value < 0 || value > 1 {
			return h.respondError(s, i, "temperature must be a number from 0.0 to 1.0")
		}
		temperature = &value
	}
	if thinking != "" && thinking != "default" && !validDiscordThinkingLevel(thinking) {
		return h.respondError(s, i, fmt.Sprintf("invalid thinking level %q", thinking))
	}
	keyIndex, parseErr := strconv.Atoi(keyText)
	if parseErr != nil || keyIndex < 1 {
		return h.respondError(s, i, "API Pool must be a positive number")
	}
	keyIndex--
	if _, err := keys.At(keyIndex); err != nil {
		return h.respondError(s, i, err.Error())
	}

	if err := session.SetProvider(provider, keys); err != nil {
		return h.respondError(s, i, err.Error())
	}
	if err := session.SetModel(model); err != nil {
		return h.respondError(s, i, err.Error())
	}
	if temperature == nil {
		if err := session.ClearTemperature(); err != nil {
			return h.respondError(s, i, err.Error())
		}
	} else if err := session.SetTemperature(*temperature); err != nil {
		return h.respondError(s, i, err.Error())
	}
	if thinking == "" || thinking == "default" {
		if err := session.ClearThinkingLevel(); err != nil {
			return h.respondError(s, i, err.Error())
		}
	} else if err := session.SetThinkingLevel(sdk.ThinkingLevel(thinking)); err != nil {
		return h.respondError(s, i, err.Error())
	}
	if err := session.SetKeyIndex(keyIndex); err != nil {
		return h.respondError(s, i, err.Error())
	}

	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: h.summary(session), Flags: discordgo.MessageFlagsEphemeral},
	})
}

func modalValues(i *discordgo.InteractionCreate) map[string]string {
	values := make(map[string]string, 6)
	for _, component := range i.ModalSubmitData().Components {
		label, ok := component.(*discordgo.Label)
		if !ok {
			if valueLabel, valueOK := component.(discordgo.Label); valueOK {
				label = &valueLabel
			} else {
				continue
			}
		}
		switch child := label.Component.(type) {
		case *discordgo.SelectMenu:
			if len(child.Values) > 0 {
				values[child.CustomID] = child.Values[0]
			}
		case discordgo.SelectMenu:
			if len(child.Values) > 0 {
				values[child.CustomID] = child.Values[0]
			}
		case *discordgo.TextInput:
			values[child.CustomID] = child.Value
		case discordgo.TextInput:
			values[child.CustomID] = child.Value
		}
	}
	return values
}

func modalSelectValues(i *discordgo.InteractionCreate) map[string]string {
	return modalValues(i)
}

func normalizeTemperatureInput(value string) string {
	value = strings.TrimSpace(value)
	if value == "default" {
		return ""
	}
	return value
}

func validDiscordThinkingLevel(level string) bool {
	switch sdk.ThinkingLevel(level) {
	case sdk.ThinkingNone, sdk.ThinkingLow, sdk.ThinkingMedium, sdk.ThinkingHigh:
		return true
	default:
		return false
	}
}

func (h *ModelSettingsHandler) hasProvider(provider sdk.ProviderID) bool {
	for _, candidate := range h.Providers {
		if candidate == provider {
			return true
		}
	}
	return false
}

func (h *ModelSettingsHandler) summary(session *sdk.Session) string {
	config := session.Config()
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

func boolPtr(value bool) *bool { return &value }
func intPtr(value int) *int { return &value }

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (h *ModelSettingsHandler) respondError(s *discordgo.Session, i *discordgo.InteractionCreate, message string) error {
	data := &discordgo.InteractionResponseData{Content: "Model settings error: " + message, Flags: discordgo.MessageFlagsEphemeral}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: data})
}
