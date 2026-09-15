package discord

import (
	"github.com/bwmarrin/discordgo"
)

// interactionAPI is the slice of the Discord API used for deferred command
// replies. *discordgo.Session satisfies it; tests inject a fake so no live
// Discord is ever required.
type interactionAPI interface {
	InteractionRespond(interaction *discordgo.Interaction, resp *discordgo.InteractionResponse, options ...discordgo.RequestOption) error
	FollowupMessageCreate(interaction *discordgo.Interaction, wait bool, data *discordgo.WebhookParams, options ...discordgo.RequestOption) (*discordgo.Message, error)
	InteractionResponseEdit(interaction *discordgo.Interaction, newresp *discordgo.WebhookEdit, options ...discordgo.RequestOption) (*discordgo.Message, error)
}

// deferEphemeralResponse acknowledges an interaction within Discord's
// 3-second window so slow work (channel creation, catalogue loads, session
// writes) can continue without the interaction expiring. Every outcome after
// a defer must go through followupEphemeral, never a second
// InteractionRespond (REQ-024).
func deferEphemeralResponse(s interactionAPI, i *discordgo.InteractionCreate) error {
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})
}

// followupEphemeral delivers the outcome of a deferred interaction as an
// ephemeral follow-up message.
func followupEphemeral(s interactionAPI, i *discordgo.InteractionCreate, content string) error {
	_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: content,
		Flags:   discordgo.MessageFlagsEphemeral,
	})
	return err
}

// followupEphemeralComponents delivers a deferred outcome that carries
// message components (for example the /session selection menu).
func followupEphemeralComponents(s interactionAPI, i *discordgo.InteractionCreate, content string, components []discordgo.MessageComponent) error {
	_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content:    content,
		Components: components,
		Flags:      discordgo.MessageFlagsEphemeral,
	})
	return err
}

// deferMessageUpdate acknowledges a message-component interaction instantly
// without a loading state, so the handler can work past the 3-second window
// and then rewrite the same message (REQ-024).
func deferMessageUpdate(s interactionAPI, i *discordgo.InteractionCreate) error {
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	})
}

// editOriginalMessage replaces the content and components of the deferred
// message, keeping one message updated instead of sending new ones.
func editOriginalMessage(s interactionAPI, i *discordgo.InteractionCreate, content string, components []discordgo.MessageComponent) error {
	_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content:    &content,
		Components: &components,
	})
	return err
}
