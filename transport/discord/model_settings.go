package discord

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

type ModelSettingsHandler struct {
	ResolveSession func(context.Context, sdk.Input) (*sdk.Session, error)
	Providers      []sdk.ProviderID
	ProviderKeys   map[sdk.ProviderID]*sdk.KeyPool
	Models         func(context.Context, sdk.ProviderID) ([]sdk.Model, error)
}

func (h *ModelSettingsHandler) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if h == nil || i == nil { return nil }
	if i.Type == discordgo.InteractionApplicationCommand {
		if i.ApplicationCommandData().Name != "model" { return nil }
		return h.respondSettings(s, i, i.ChannelID)
	}
	if i.Type != discordgo.InteractionMessageComponent { return nil }
	customID := i.MessageComponentData().CustomID
	if !strings.HasPrefix(customID, "model:") { return nil }
	return h.handleSelection(s, i, customID)
}

func (h *ModelSettingsHandler) respondSettings(s *discordgo.Session, i *discordgo.InteractionCreate, channelID string) error {
	if h.ResolveSession == nil { return h.respondError(s, i, "session manager is not configured") }
	session, err := h.ResolveSession(context.Background(), sdk.Input{SessionID: "discord:channel:" + channelID})
	if err != nil { return h.respondError(s, i, err.Error()) }
	components, err := h.components(context.Background(), session)
	if err != nil { return h.respondError(s, i, err.Error()) }
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: h.summary(session), Flags: discordgo.MessageFlagsEphemeral, Components: components}})
}

func (h *ModelSettingsHandler) handleSelection(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) error {
	parts := strings.SplitN(customID, ":", 4)
	if len(parts) != 4 { return h.respondError(s, i, "invalid model settings control") }
	field, channelID, value := parts[1], parts[2], parts[3]
	if h.ResolveSession == nil { return h.respondError(s, i, "session manager is not configured") }
	session, err := h.ResolveSession(context.Background(), sdk.Input{SessionID: "discord:channel:" + channelID})
	if err != nil { return h.respondError(s, i, err.Error()) }
	switch field {
	case "provider": err = session.SetProvider(sdk.ProviderID(value), h.ProviderKeys[sdk.ProviderID(value)])
	case "model": err = session.SetModel(value)
	case "thinking": err = session.SetThinkingLevel(sdk.ThinkingLevel(value))
	case "temperature":
		var temperature float64
		temperature, err = strconv.ParseFloat(value, 64)
		if err == nil { err = session.SetTemperature(temperature) }
	case "key":
		var index int
		index, err = strconv.Atoi(value)
		if err == nil { err = session.SetKeyIndex(index) }
	default: return h.respondError(s, i, "unknown model settings control")
	}
	if err != nil { return h.respondError(s, i, err.Error()) }
	components, err := h.components(context.Background(), session)
	if err != nil { return h.respondError(s, i, err.Error()) }
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: h.summary(session), Components: components}})
}

func (h *ModelSettingsHandler) components(ctx context.Context, session *sdk.Session) ([]discordgo.MessageComponent, error) {
	config := session.Config()
	rows := []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{h.selectMenu("provider", "Provider", string(config.Provider), session.ID(), providerOptions(h.Providers))}}}
	models := []sdk.Model{}
	if h.Models != nil && config.Provider != "" {
		var err error
		models, err = h.Models(ctx, config.Provider)
		if err != nil { return nil, fmt.Errorf("load models: %w", err) }
	}
	modelOptions := make([]discordgo.SelectMenuOption, 0, len(models))
	for _, model := range models {
		label := model.Name
		if label == "" { label = model.ID }
		modelOptions = append(modelOptions, discordgo.SelectMenuOption{Label: truncate(label, 100), Value: model.ID, Default: model.ID == config.Model})
	}
	if len(modelOptions) == 0 && config.Model != "" { modelOptions = append(modelOptions, discordgo.SelectMenuOption{Label: truncate(config.Model, 100), Value: config.Model, Default: true}) }
	rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{h.selectMenu("model", "Model", config.Model, session.ID(), modelOptions)}})
	thinkingOptions := []discordgo.SelectMenuOption{}
	for _, level := range []sdk.ThinkingLevel{sdk.ThinkingNone, sdk.ThinkingLow, sdk.ThinkingMedium, sdk.ThinkingHigh} { thinkingOptions = append(thinkingOptions, discordgo.SelectMenuOption{Label: string(level), Value: string(level), Default: config.ThinkingLevel == level}) }
	rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{h.selectMenu("thinking", "Thinking", string(config.ThinkingLevel), session.ID(), thinkingOptions)}})
	temperatureOptions := make([]discordgo.SelectMenuOption, 0, 11)
	for n := 0; n <= 10; n++ { v := float64(n)/10; value := strconv.FormatFloat(v, 'f', 1, 64); temperatureOptions = append(temperatureOptions, discordgo.SelectMenuOption{Label: value, Value: value, Default: config.Temperature != nil && *config.Temperature == v}) }
	rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{h.selectMenu("temperature", "Temperature", temperatureLabel(config.Temperature), session.ID(), temperatureOptions)}})
	keyOptions := []discordgo.SelectMenuOption{}
	if keys := h.ProviderKeys[config.Provider]; keys != nil { for index := 0; index < keys.Len(); index++ { keyOptions = append(keyOptions, discordgo.SelectMenuOption{Label: fmt.Sprintf("API Pool %d", index+1), Value: strconv.Itoa(index), Default: index == config.KeyIndex}) } }
	rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{h.selectMenu("key", "API Pool", strconv.Itoa(config.KeyIndex), session.ID(), keyOptions)}})
	return rows, nil
}

func (h *ModelSettingsHandler) selectMenu(field, placeholder, value, sessionID string, options []discordgo.SelectMenuOption) discordgo.SelectMenu {
	channelID := strings.TrimPrefix(sessionID, "discord:channel:")
	if len(options) > 25 { options = options[:25] }
	return discordgo.SelectMenu{CustomID: "model:" + field + ":" + channelID + ":" + value, Placeholder: placeholder, Options: options, MinValues: 1, MaxValues: 1}
}

func providerOptions(providers []sdk.ProviderID) []discordgo.SelectMenuOption { options := make([]discordgo.SelectMenuOption, 0, len(providers)); for _, provider := range providers { options = append(options, discordgo.SelectMenuOption{Label: string(provider), Value: string(provider)}) }; return options }

func (h *ModelSettingsHandler) summary(session *sdk.Session) string {
	config := session.Config(); thinking := string(config.ThinkingLevel); if thinking == "" { thinking = "default" }; model := config.Model; if model == "" { model = "not set" }
	return fmt.Sprintf("**Session Model Settings**\nProvider: `%s`\nModel: `%s`\nThinking: `%s`\nTemperature: `%s`\nAPI Pool: `%d`", config.Provider, model, thinking, temperatureLabel(config.Temperature), config.KeyIndex+1)
}

func temperatureLabel(value *float64) string { if value == nil { return "default" }; return strconv.FormatFloat(*value, 'f', 1, 64) }
func truncate(value string, max int) string { runes := []rune(value); if len(runes) <= max { return value }; return string(runes[:max-1]) + "…" }

func (h *ModelSettingsHandler) respondError(s *discordgo.Session, i *discordgo.InteractionCreate, message string) error {
	data := &discordgo.InteractionResponseData{Content: "Model settings error: " + message, Flags: discordgo.MessageFlagsEphemeral}
	if i.Type == discordgo.InteractionMessageComponent { return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: data}) }
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: data})
}
