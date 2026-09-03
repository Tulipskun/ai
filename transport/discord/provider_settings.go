package discord

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

const providerSettingsModalID = "provider:settings"

type ProviderSettingsHandler struct {
	Adapters []sdk.AdapterID
	Upsert   func(context.Context, string, string, string, string) error
}

func (h *ProviderSettingsHandler) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if h == nil || i == nil { return nil }
	if i.Type == discordgo.InteractionApplicationCommand {
		if i.ApplicationCommandData().Name != "provider" { return nil }
		return h.openModal(s, i)
	}
	if i.Type != discordgo.InteractionModalSubmit || i.ModalSubmitData().CustomID != providerSettingsModalID { return nil }
	return h.submit(s, i)
}

func (h *ProviderSettingsHandler) modalData() *discordgo.InteractionResponseData {
	options := make([]discordgo.SelectMenuOption, 0, len(h.Adapters))
	for _, adapter := range h.Adapters {
		value := strings.TrimSpace(string(adapter))
		if value == "" || len(options) >= 25 { continue }
		options = append(options, discordgo.SelectMenuOption{Label: value, Value: value})
	}
	return &discordgo.InteractionResponseData{
		CustomID: providerSettingsModalID,
		Title: "Provider Settings",
		Components: []discordgo.MessageComponent{
			discordgo.Label{Label: "Name", Description: "Provider name", Component: discordgo.TextInput{CustomID: "name", Style: discordgo.TextInputShort, Placeholder: "B.ai", Required: boolPtr(true), MaxLength: 100}},
			discordgo.Label{Label: "Adapter", Description: "Protocol adapter", Component: discordgo.SelectMenu{CustomID: "adapter", MenuType: discordgo.StringSelectMenu, Placeholder: "Select adapter", Options: options, Required: boolPtr(true)}},
			discordgo.Label{Label: "URL", Description: "Provider API base URL", Component: discordgo.TextInput{CustomID: "url", Style: discordgo.TextInputShort, Placeholder: "https://api.example.com/v1", Required: boolPtr(true), MaxLength: 500}},
			discordgo.Label{Label: "API Key", Description: "Provider API key", Component: discordgo.TextInput{CustomID: "api_key", Style: discordgo.TextInputShort, Placeholder: "API key", Required: boolPtr(true), MaxLength: 500}},
		},
	}
}

func (h *ProviderSettingsHandler) openModal(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if len(h.Adapters) == 0 { return respondProviderError(s, i, "no adapters are available") }
	data := h.modalData()
	if len(data.Components) < 4 { return respondProviderError(s, i, "no adapters are available") }
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseModal, Data: data})
}

func (h *ProviderSettingsHandler) submit(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if h.Upsert == nil { return respondProviderError(s, i, "provider manager is not configured") }
	values := modalValues(i)
	name := strings.TrimSpace(values["name"]); adapter := strings.TrimSpace(strings.ToLower(values["adapter"])); endpoint := strings.TrimSpace(values["url"]); apiKey := strings.TrimSpace(values["api_key"])
	if name == "" || adapter == "" || endpoint == "" || apiKey == "" { return respondProviderError(s, i, "name, adapter, URL, and API key are required") }
	if err := h.Upsert(context.Background(), name, adapter, endpoint, apiKey); err != nil { return respondProviderError(s, i, err.Error()) }
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: fmt.Sprintf("Provider `%s` saved and model catalogue refreshed.", name), Flags: discordgo.MessageFlagsEphemeral}})
}

func respondProviderError(s *discordgo.Session, i *discordgo.InteractionCreate, message string) error {
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "Provider settings error: " + message, Flags: discordgo.MessageFlagsEphemeral}})
}
