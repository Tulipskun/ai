package discord

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

const modelSettingsPrefix = "model:settings:"

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
		return h.respondSettings(s, i, i.ChannelID)
	}
	if i.Type == discordgo.InteractionModalSubmit && strings.HasPrefix(i.ModalSubmitData().CustomID, modelSettingsPrefix) {
		return h.handleSettingsSubmit(s, i, i.ModalSubmitData().CustomID)
	}
	return nil
}

func (h *ModelSettingsHandler) respondSettings(s *discordgo.Session, i *discordgo.InteractionCreate, channelID string) error {
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
	config := session.Config()
	provider := config.Provider
	if provider == "" || !h.hasProvider(provider) {
		provider = h.Providers[0]
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
	keyCount := 1
	if keys := h.ProviderKeys[provider]; keys != nil && keys.Len() > 0 {
		keyCount = keys.Len()
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: modelSettingsModalWithCatalog(session.ID(), string(provider), config.Model, temperatureLabel(config.Temperature), string(config.ThinkingLevel), strconv.Itoa(config.KeyIndex+1), h.Providers, models, keyCount),
	})
}

func modelSettingsModal(sessionID, provider, model, temperature, thinking, key string) *discordgo.InteractionResponseData {
	models := make([]sdk.Model, 0, 1)
	if model != "" {
		models = append(models, sdk.Model{ID: model})
	}
	keyCount := 1
	if parsed, err := strconv.Atoi(key); err == nil && parsed > keyCount {
		keyCount = parsed
	}
	return modelSettingsModalWithCatalog(sessionID, provider, model, temperature, thinking, key, []sdk.ProviderID{sdk.ProviderID(provider)}, models, keyCount)
}

func modelSettingsModalWithCatalog(sessionID, provider, model, temperature, thinking, key string, providers []sdk.ProviderID, models []sdk.Model, keyCount int) *discordgo.InteractionResponseData {
	channelID := strings.TrimPrefix(sessionID, "discord:channel:")
	providerOptions := makeProviderOptions(providers, provider)
	modelOptions := makeModelOptions(models, model)
	thinkingOptions := []discordgo.SelectMenuOption{
		{Label: "Default", Value: "default", Default: thinking == ""},
		{Label: "None", Value: string(sdk.ThinkingNone), Default: thinking == string(sdk.ThinkingNone)},
		{Label: "Low", Value: string(sdk.ThinkingLow), Default: thinking == string(sdk.ThinkingLow)},
		{Label: "Medium", Value: string(sdk.ThinkingMedium), Default: thinking == string(sdk.ThinkingMedium)},
		{Label: "High", Value: string(sdk.ThinkingHigh), Default: thinking == string(sdk.ThinkingHigh)},
	}
	temperatureOptions := makeTemperatureOptions(temperature)
	keyOptions := makeKeyOptions(keyCount, key)

	return &discordgo.InteractionResponseData{
		CustomID: modelSettingsPrefix + channelID,
		Title:    "Model Settings",
		Components: []discordgo.MessageComponent{
			discordgo.Label{Label: "Provider", Description: "Choose the AI provider.", Component: discordgo.SelectMenu{CustomID: "provider", MenuType: discordgo.StringSelectMenu, Placeholder: "Select provider", Options: providerOptions, Required: boolPtr(true)}},
			discordgo.Label{Label: "Model", Description: "Choose a model from the provider catalogue.", Component: discordgo.SelectMenu{CustomID: "model", MenuType: discordgo.StringSelectMenu, Placeholder: "Select model", Options: modelOptions, Required: boolPtr(true)}},
			discordgo.Label{Label: "Temperature", Component: discordgo.SelectMenu{CustomID: "temperature", MenuType: discordgo.StringSelectMenu, Placeholder: "Select temperature", Options: temperatureOptions, Required: boolPtr(true)}},
			discordgo.Label{Label: "Thinking", Component: discordgo.SelectMenu{CustomID: "thinking", MenuType: discordgo.StringSelectMenu, Placeholder: "Select thinking level", Options: thinkingOptions, Required: boolPtr(true)}},
			discordgo.Label{Label: "API Pool", Component: discordgo.SelectMenu{CustomID: "key", MenuType: discordgo.StringSelectMenu, Placeholder: "Select API key pool", Options: keyOptions, Required: boolPtr(true)}},
		},
	}
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
	options := make([]discordgo.SelectMenuOption, 0, minInt(len(models), 25))
	currentIncluded := false
	for _, model := range models {
		if model.ID == "" {
			continue
		}
		if model.ID == current {
			currentIncluded = true
		}
		if len(options) >= 25 {
			continue
		}
		options = append(options, discordgo.SelectMenuOption{Label: model.ID, Value: model.ID, Default: model.ID == current})
	}
	if current != "" && !currentIncluded && len(options) == 25 {
		options[24] = discordgo.SelectMenuOption{Label: current, Value: current, Default: true}
	}
	return options
}

func makeTemperatureOptions(current string) []discordgo.SelectMenuOption {
	values := []string{"default", "0.0", "0.2", "0.4", "0.6", "0.8", "1.0"}
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
	channelID := strings.TrimPrefix(customID, modelSettingsPrefix)
	if channelID == "" || h.ResolveSession == nil {
		return h.respondError(s, i, "invalid model settings session")
	}
	session, err := h.ResolveSession(context.Background(), sdk.Input{SessionID: "discord:channel:" + channelID})
	if err != nil {
		return h.respondError(s, i, err.Error())
	}

	values := modalSelectValues(i)
	provider := sdk.ProviderID(strings.TrimSpace(values["provider"]))
	model := strings.TrimSpace(values["model"])
	thinking := strings.TrimSpace(strings.ToLower(values["thinking"]))
	temperatureText := strings.TrimSpace(values["temperature"])
	keyText := strings.TrimSpace(values["key"])

	if provider == "" {
		return h.respondError(s, i, "provider is required")
	}
	if !h.hasProvider(provider) {
		return h.respondError(s, i, fmt.Sprintf("unknown provider %q", provider))
	}
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
		Data: &discordgo.InteractionResponseData{
			Content: h.summary(session),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func modalSelectValues(i *discordgo.InteractionCreate) map[string]string {
	values := make(map[string]string, 5)
	for _, component := range i.ModalSubmitData().Components {
		label, ok := component.(*discordgo.Label)
		if !ok {
			if valueLabel, valueOK := component.(discordgo.Label); valueOK {
				label = &valueLabel
			} else {
				continue
			}
		}
		selectMenu, ok := label.Component.(*discordgo.SelectMenu)
		if !ok {
			if valueSelect, valueOK := label.Component.(discordgo.SelectMenu); valueOK {
				selectMenu = &valueSelect
			} else {
				continue
			}
		}
		if len(selectMenu.Values) > 0 {
			values[selectMenu.CustomID] = selectMenu.Values[0]
		}
	}
	return values
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
