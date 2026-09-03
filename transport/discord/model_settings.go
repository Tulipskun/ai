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
	session, err := h.ResolveSession(context.Background(), sdk.Input{SessionID: "discord:channel:" + channelID})
	if err != nil {
		return h.respondError(s, i, err.Error())
	}
	config := session.Config()
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: modelSettingsModal(session.ID(), config.Provider, config.Model, temperatureLabel(config.Temperature), string(config.ThinkingLevel), strconv.Itoa(config.KeyIndex+1)),
	})
}

func modelSettingsModal(sessionID, provider, model, temperature, thinking, key string) *discordgo.InteractionResponseData {
	channelID := strings.TrimPrefix(sessionID, "discord:channel:")
	if temperature == "default" {
		temperature = ""
	}
	if thinking == "" {
		thinking = string(sdk.ThinkingMedium)
	}
	if key == "0" || key == "" {
		key = "1"
	}
	return &discordgo.InteractionResponseData{
		CustomID: modelSettingsPrefix + channelID,
		Title:    "Model Settings",
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.TextInput{CustomID: "provider", Label: "Provider", Style: discordgo.TextInputShort, Placeholder: "e.g. google", Value: provider, Required: true, MaxLength: 100},
			}},
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.TextInput{CustomID: "model", Label: "Model", Style: discordgo.TextInputShort, Placeholder: "e.g. gemini-2.5-flash", Value: model, Required: true, MaxLength: 200},
			}},
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.TextInput{CustomID: "temperature", Label: "Temperature", Style: discordgo.TextInputShort, Placeholder: "0.0 - 1.0", Value: temperature, Required: false, MaxLength: 20},
			}},
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.TextInput{CustomID: "thinking", Label: "Thinking", Style: discordgo.TextInputShort, Placeholder: "none / low / medium / high", Value: thinking, Required: false, MaxLength: 20},
			}},
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.TextInput{CustomID: "key", Label: "API Pool", Style: discordgo.TextInputShort, Placeholder: "1", Value: key, Required: true, MaxLength: 20},
			}},
		},
	}
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

	values := modalTextValues(i)
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
	if temperatureText != "" {
		value, parseErr := strconv.ParseFloat(temperatureText, 64)
		if parseErr != nil || value < 0 || value > 1 {
			return h.respondError(s, i, "temperature must be a number from 0.0 to 1.0")
		}
		temperature = &value
	}

	if thinking == "" {
		thinking = string(sdk.ThinkingMedium)
	}
	if !validDiscordThinkingLevel(thinking) {
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
	if err := session.SetThinkingLevel(sdk.ThinkingLevel(thinking)); err != nil {
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

func modalTextValues(i *discordgo.InteractionCreate) map[string]string {
	values := make(map[string]string, 5)
	for _, row := range i.ModalSubmitData().Components {
		actionRow, ok := row.(*discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, component := range actionRow.Components {
			input, ok := component.(*discordgo.TextInput)
			if ok {
				values[input.CustomID] = input.Value
			}
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

func (h *ModelSettingsHandler) respondError(s *discordgo.Session, i *discordgo.InteractionCreate, message string) error {
	data := &discordgo.InteractionResponseData{Content: "Model settings error: " + message, Flags: discordgo.MessageFlagsEphemeral}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: data})
}
