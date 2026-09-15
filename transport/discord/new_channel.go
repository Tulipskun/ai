package discord

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

// newChannelNamePrefix prefixes every channel created by /new so automated
// channels stay distinguishable from hand-made ones.
const newChannelNamePrefix = "ai-"

// newChannelDiscord is the slice of the Discord API the /new handler needs.
// *discordgo.Session satisfies it; tests inject a fake so no live Discord is
// ever required (REQ-027).
type newChannelDiscord interface {
	interactionAPI
	GuildChannelCreate(guildID, name string, ctype discordgo.ChannelType, options ...discordgo.RequestOption) (*discordgo.Channel, error)
	ChannelMessageSend(channelID, content string, options ...discordgo.RequestOption) (*discordgo.Message, error)
}

// NewChannelHandler implements /new: it creates a text channel in the same
// guild named with the current date and time, clones the invoking channel's
// model settings into the new channel's session, and reports those settings
// inside the new channel (REQ-027). Everything here stays inside the Discord
// module: sessions are resolved, never rebuilt, and no canonical contract
// changes.
type NewChannelHandler struct {
	ResolveSession    func(context.Context, sdk.Input) (*sdk.Session, error)
	SessionForChannel func(channelID string) string
	ProviderKeys      map[sdk.ProviderID]*sdk.KeyPool
	Now               func() time.Time
}

// newChannelName renders the date-time channel name. Only lowercase digits
// and dashes are used, which Discord channel names always accept.
func newChannelName(now time.Time) string {
	return newChannelNamePrefix + now.Format("2006-01-02-1504")
}

func (h *NewChannelHandler) now() time.Time {
	if h != nil && h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *NewChannelHandler) sessionIDFor(channelID string) string {
	channelID = strings.TrimSpace(channelID)
	if h != nil && h.SessionForChannel != nil {
		if id := strings.TrimSpace(h.SessionForChannel(channelID)); id != "" {
			return id
		}
	}
	return "discord:channel:" + channelID
}

// sourceConfig reads the invoking channel's model settings and refuses to
// proceed when there is nothing to clone, so a failed invocation never leaves
// an orphan channel behind.
func (h *NewChannelHandler) sourceConfig(ctx context.Context, sourceSessionID string) (sdk.SessionConfig, *sdk.KeyPool, error) {
	if h.ResolveSession == nil {
		return sdk.SessionConfig{}, nil, fmt.Errorf("session manager is not configured")
	}
	if err := ctx.Err(); err != nil {
		return sdk.SessionConfig{}, nil, err
	}
	source, err := h.ResolveSession(ctx, sdk.Input{SessionID: sourceSessionID})
	if err != nil {
		return sdk.SessionConfig{}, nil, err
	}
	config := source.Config()
	if strings.TrimSpace(string(config.Provider)) == "" || strings.TrimSpace(config.Model) == "" {
		return sdk.SessionConfig{}, nil, fmt.Errorf("this channel has no model settings yet; configure them with /model first")
	}
	keys := h.ProviderKeys[config.Provider]
	if keys == nil {
		return sdk.SessionConfig{}, nil, fmt.Errorf("provider %q has no API key pool", config.Provider)
	}
	return config, keys, nil
}

// applySettings copies validated source settings into the new channel's
// session. SetProvider runs first because it resets the model and key index
// it then re-applies.
func (h *NewChannelHandler) applySettings(ctx context.Context, targetSessionID string, config sdk.SessionConfig, keys *sdk.KeyPool) (sdk.SessionConfig, error) {
	target, err := h.ResolveSession(ctx, sdk.Input{SessionID: targetSessionID})
	if err != nil {
		return sdk.SessionConfig{}, err
	}
	if err := target.SetProvider(config.Provider, keys); err != nil {
		return sdk.SessionConfig{}, err
	}
	if err := target.SetModel(config.Model); err != nil {
		return sdk.SessionConfig{}, err
	}
	if config.Temperature == nil {
		if err := target.ClearTemperature(); err != nil {
			return sdk.SessionConfig{}, err
		}
	} else if err := target.SetTemperature(*config.Temperature); err != nil {
		return sdk.SessionConfig{}, err
	}
	if config.ThinkingLevel == "" {
		if err := target.ClearThinkingLevel(); err != nil {
			return sdk.SessionConfig{}, err
		}
	} else if err := target.SetThinkingLevel(config.ThinkingLevel); err != nil {
		return sdk.SessionConfig{}, err
	}
	if err := target.SetKeyIndex(config.KeyIndex); err != nil {
		return sdk.SessionConfig{}, err
	}
	if err := target.SetAgentMode(panelAgentMode(config)); err != nil {
		return sdk.SessionConfig{}, err
	}
	return target.Config(), nil
}

func (h *NewChannelHandler) respondEphemeral(s newChannelDiscord, i *discordgo.InteractionCreate, message string) error {
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: message, Flags: discordgo.MessageFlagsEphemeral},
	})
}

// Handle serves the /new application command. Fast validation failures answer
// immediately; everything from channel creation onward runs behind a deferred
// ephemeral response with outcomes delivered as follow-ups, so slow Discord
// or session work cannot expire the interaction (REQ-024). Failures never
// create a session for a channel that does not exist (REQ-027).
func (h *NewChannelHandler) Handle(s newChannelDiscord, i *discordgo.InteractionCreate) error {
	if h == nil || s == nil || i == nil {
		return nil
	}
	if i.Type != discordgo.InteractionApplicationCommand || i.ApplicationCommandData().Name != "new" {
		return nil
	}
	guildID := strings.TrimSpace(i.GuildID)
	if guildID == "" {
		return h.respondEphemeral(s, i, "Use /new inside a server text channel; direct messages have no server to create a channel in.")
	}
	sourceChannelID := strings.TrimSpace(i.ChannelID)
	if sourceChannelID == "" {
		return h.respondEphemeral(s, i, "Could not tell which channel invoked /new.")
	}
	config, keys, err := h.sourceConfig(context.Background(), h.sessionIDFor(sourceChannelID))
	if err != nil {
		return h.respondEphemeral(s, i, err.Error())
	}
	if err := deferEphemeralResponse(s, i); err != nil {
		return err
	}
	channel, err := s.GuildChannelCreate(guildID, newChannelName(h.now()), discordgo.ChannelTypeGuildText)
	if err != nil || channel == nil || strings.TrimSpace(channel.ID) == "" {
		return followupEphemeral(s, i, "Could not create the channel; the bot needs the Manage Channels permission.")
	}
	applied, err := h.applySettings(context.Background(), h.sessionIDFor(channel.ID), config, keys)
	if err != nil {
		return followupEphemeral(s, i, "Channel created, but settings were not copied: "+err.Error())
	}
	if _, err := s.ChannelMessageSend(channel.ID, sessionSettingsSummary(applied)); err != nil {
		return followupEphemeral(s, i, fmt.Sprintf("Channel created as <#%s> with the same model settings, but the summary message could not be delivered.", channel.ID))
	}
	return followupEphemeral(s, i, fmt.Sprintf("Created <#%s> with the same model settings as this channel.", channel.ID))
}
