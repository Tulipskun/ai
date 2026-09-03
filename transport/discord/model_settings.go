package discord

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

const modelSearchPrefix = "model:search:"

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
	if i.Type == discordgo.InteractionMessageComponent {
		customID := i.MessageComponentData().CustomID
		if strings.HasPrefix(customID, "model:") {
			return h.handleSelection(s, i, customID)
		}
		return nil
	}
	if i.Type == discordgo.InteractionModalSubmit {
		if strings.HasPrefix(i.ModalSubmitData().CustomID, modelSearchPrefix) {
			return h.handleModelSearch(s, i, i.ModalSubmitData().CustomID)
		}
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
	components, err := h.components(context.Background(), session)
	if err != nil {
		return h.respondError(s, i, err.Error())
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:    h.summary(session),
			Flags:      discordgo.MessageFlagsEphemeral,
			Components: components,
		},
	})
}

func (h *ModelSettingsHandler) handleSelection(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) error {
	parts := strings.SplitN(customID, ":", 3)
	data := i.MessageComponentData()
	if len(parts) != 3 {
		return h.respondError(s, i, "invalid model settings control")
	}
	if parts[1] == "search" {
		return h.respondModelSearchModal(s, i, parts[2])
	}
	if len(data.Values) != 1 {
		return h.respondError(s, i, "invalid model settings selection")
	}

	field, channelID, value := parts[1], parts[2], data.Values[0]
	session, err := h.ResolveSession(context.Background(), sdk.Input{SessionID: "discord:channel:" + channelID})
	if err != nil {
		return h.respondError(s, i, err.Error())
	}

	switch field {
	case "provider":
		provider := sdk.ProviderID(value)
		err = session.SetProvider(provider, h.ProviderKeys[provider])
	case "model":
		if value == "__unavailable" {
			return h.respondError(s, i, "no model selected")
		}
		err = session.SetModel(value)
	case "thinking":
		err = session.SetThinkingLevel(sdk.ThinkingLevel(value))
	case "temperature":
		var v float64
		v, err = strconv.ParseFloat(value, 64)
		if err == nil {
			err = session.SetTemperature(v)
		}
	case "key":
		var index int
		index, err = strconv.Atoi(value)
		if err == nil {
			err = session.SetKeyIndex(index)
		}
	default:
		return h.respondError(s, i, "unknown model settings control")
	}
	if err != nil {
		return h.respondError(s, i, err.Error())
	}

	components, err := h.components(context.Background(), session)
	if err != nil {
		return h.respondError(s, i, err.Error())
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Content: h.summary(session), Components: components},
	})
}

func (h *ModelSettingsHandler) respondModelSearchModal(s *discordgo.Session, i *discordgo.InteractionCreate, channelID string) error {
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: modelSearchPrefix + channelID,
			Title:    "Search Models",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{CustomID: "query", Label: "Model name", Style: discordgo.TextInputShort, Placeholder: "e.g. gemini-2.5-flash", Required: false, MaxLength: 100},
				}},
			},
		},
	})
}

func (h *ModelSettingsHandler) handleModelSearch(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) error {
	channelID := strings.TrimPrefix(customID, modelSearchPrefix)
	if channelID == "" || h.ResolveSession == nil {
		return h.respondError(s, i, "invalid model search session")
	}
	session, err := h.ResolveSession(context.Background(), sdk.Input{SessionID: "discord:channel:" + channelID})
	if err != nil {
		return h.respondError(s, i, err.Error())
	}
	provider := session.Config().Provider
	if provider == "" {
		return h.respondError(s, i, "select a provider first")
	}
	if h.Models == nil {
		return h.respondError(s, i, "model catalog is not configured")
	}

	query := ""
	for _, row := range i.ModalSubmitData().Components {
		if actionRow, ok := row.(*discordgo.ActionsRow); ok {
			for _, component := range actionRow.Components {
				if input, ok := component.(*discordgo.TextInput); ok && input.CustomID == "query" {
					query = input.Value
				}
			}
		}
	}
	models, err := h.Models(context.Background(), provider)
	if err != nil {
		return h.respondError(s, i, fmt.Errorf("load models: %w", err).Error())
	}
	matches := filterModels(models, query)
	if len(matches) == 0 {
		return h.respondError(s, i, "no models match that search")
	}
	options := modelSelectOptions(matches, session.Config().Model)
	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{CustomID: "model:model:" + channelID, Placeholder: fmt.Sprintf("Select model (%d matches)", len(matches)), Options: options, MinValues: intPtr(1), MaxValues: 1},
		}},
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:    fmt.Sprintf("Model search: `%s` — showing %d of %d matches", query, len(options), len(matches)),
			Flags:      discordgo.MessageFlagsEphemeral,
			Components: components,
		},
	})
}

func (h *ModelSettingsHandler) components(ctx context.Context, session *sdk.Session) ([]discordgo.MessageComponent, error) {
	config := session.Config()
	rows := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{h.selectMenu("provider", "Provider", session.ID(), providerOptions(h.Providers), false)}},
	}
	modelLabel := "Search model"
	if config.Model != "" {
		modelLabel = "Search model (current: " + truncate(config.Model, 70) + ")"
	}
	modelDisabled := config.Provider == ""
	rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{CustomID: "model:search:" + strings.TrimPrefix(session.ID(), "discord:channel:"), Label: modelLabel, Style: discordgo.PrimaryButton, Disabled: modelDisabled},
	}})

	thinkingOptions := []discordgo.SelectMenuOption{}
	for _, level := range []sdk.ThinkingLevel{sdk.ThinkingNone, sdk.ThinkingLow, sdk.ThinkingMedium, sdk.ThinkingHigh} {
		thinkingOptions = append(thinkingOptions, discordgo.SelectMenuOption{Label: string(level), Value: string(level), Default: config.ThinkingLevel == level})
	}
	rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{h.selectMenu("thinking", "Thinking", session.ID(), thinkingOptions, false)}})

	temperatureOptions := make([]discordgo.SelectMenuOption, 0, 11)
	for n := 0; n <= 10; n++ {
		v := float64(n) / 10
		value := strconv.FormatFloat(v, 'f', 1, 64)
		temperatureOptions = append(temperatureOptions, discordgo.SelectMenuOption{Label: value, Value: value, Default: config.Temperature != nil && *config.Temperature == v})
	}
	rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{h.selectMenu("temperature", "Temperature", session.ID(), temperatureOptions, false)}})

	keyOptions := []discordgo.SelectMenuOption{}
	if keys := h.ProviderKeys[config.Provider]; keys != nil {
		for index := 0; index < keys.Len(); index++ {
			keyOptions = append(keyOptions, discordgo.SelectMenuOption{Label: fmt.Sprintf("API Pool %d", index+1), Value: strconv.Itoa(index), Default: index == config.KeyIndex})
		}
	}
	keyDisabled := len(keyOptions) == 0
	if keyDisabled {
		keyOptions = []discordgo.SelectMenuOption{{Label: "Select a provider first", Value: "__unavailable"}}
	}
	rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{h.selectMenu("key", "API Pool", session.ID(), keyOptions, keyDisabled)}})
	return rows, nil
}

func filterModels(models []sdk.Model, query string) []sdk.Model {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return append([]sdk.Model(nil), models...)
	}
	matches := make([]sdk.Model, 0)
	for _, model := range models {
		name := model.Name
		if name == "" {
			name = model.ID
		}
		if strings.Contains(strings.ToLower(name), query) || strings.Contains(strings.ToLower(model.ID), query) {
			matches = append(matches, model)
		}
	}
	return matches
}

func modelSelectOptions(models []sdk.Model, current string) []discordgo.SelectMenuOption {
	options := make([]discordgo.SelectMenuOption, 0, minInt(len(models), 25))
	for _, model := range models {
		label := model.Name
		if label == "" {
			label = model.ID
		}
		options = append(options, discordgo.SelectMenuOption{Label: truncate(label, 100), Value: model.ID, Default: model.ID == current})
		if len(options) == 25 {
			break
		}
	}
	return options
}

func (h *ModelSettingsHandler) selectMenu(field, placeholder, sessionID string, options []discordgo.SelectMenuOption, disabled bool) discordgo.SelectMenu {
	channelID := strings.TrimPrefix(sessionID, "discord:channel:")
	if len(options) > 25 {
		options = options[:25]
	}
	min, max := 1, 1
	return discordgo.SelectMenu{CustomID: "model:" + field + ":" + channelID, Placeholder: placeholder, Options: options, MinValues: &min, MaxValues: max, Disabled: disabled}
}

func providerOptions(providers []sdk.ProviderID) []discordgo.SelectMenuOption {
	options := make([]discordgo.SelectMenuOption, 0, len(providers))
	for _, provider := range providers {
		options = append(options, discordgo.SelectMenuOption{Label: string(provider), Value: string(provider)})
	}
	return options
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

func truncate(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max-1]) + "…"
}

func intPtr(value int) *int { return &value }

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (h *ModelSettingsHandler) respondError(s *discordgo.Session, i *discordgo.InteractionCreate, message string) error {
	data := &discordgo.InteractionResponseData{Content: "Model settings error: " + message, Flags: discordgo.MessageFlagsEphemeral}
	if i.Type == discordgo.InteractionMessageComponent || i.Type == discordgo.InteractionModalSubmit {
		return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: data})
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: data})
}
