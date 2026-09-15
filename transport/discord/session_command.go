package discord

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

type SessionCommandHandler struct {
	ListSessions   func(int) ([]sdk.SessionInfo, error)
	SelectSession  func(string, string) error
}

func (h *SessionCommandHandler) Handle(s interactionAPI, i *discordgo.InteractionCreate) error {
	if h == nil || i == nil {
		return nil
	}
	if i.Type == discordgo.InteractionApplicationCommand && i.ApplicationCommandData().Name == "session" {
		if h.ListSessions == nil {
			return respondError(s, i, "session manager is not configured")
		}
		// Listing sessions can outlast the 3-second interaction window, so
		// defer first and deliver the selection menu as a follow-up (REQ-024).
		if err := deferEphemeralResponse(s, i); err != nil {
			return err
		}
		items, err := h.ListSessions(25)
		if err != nil {
			return followupEphemeral(s, i, err.Error())
		}
		if len(items) == 0 {
			return followupEphemeral(s, i, "no sessions")
		}
		options := make([]discordgo.SelectMenuOption, 0, len(items))
		for _, item := range items {
			label := item.ID
			if len(label) > 100 { label = label[:100] }
			description := fmt.Sprintf("%s / %s", item.Provider, item.Model)
			if len(description) > 100 { description = description[:100] }
			options = append(options, discordgo.SelectMenuOption{Label: label, Value: item.ID, Description: description})
		}
		minValues := 1
		return followupEphemeralComponents(s, i, "Select a session:", []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{CustomID: "session:select", MenuType: discordgo.StringSelectMenu, Placeholder: "Select session", Options: options, MinValues: &minValues, MaxValues: 1},
		}}})
	}
	if i.Type != discordgo.InteractionMessageComponent || i.MessageComponentData().CustomID != "session:select" {
		return nil
	}
	values := i.MessageComponentData().Values
	if len(values) != 1 || values[0] == "" {
		return respondError(s, i, "session is required")
	}
	if h.SelectSession == nil {
		return respondError(s, i, "session selector is not configured")
	}
	if err := h.SelectSession(i.ChannelID, values[0]); err != nil {
		return respondError(s, i, err.Error())
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Content: fmt.Sprintf("Session selected: `%s`", values[0]), Components: []discordgo.MessageComponent{}},
	})
}

func (h *SessionCommandHandler) Context(_ context.Context) {}

func respondError(s interactionAPI, i *discordgo.InteractionCreate, message string) error {
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: message,
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
}
