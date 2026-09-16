package discord

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tulipskun/ai/sdk"
	"github.com/bwmarrin/discordgo"
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

// sourceConfigs reads the invoking channel's model settings for both agent
// sides and refuses to proceed when there is nothing to clone, so a failed
// invocation never leaves an orphan channel behind.
func (h *NewChannelHandler) sourceConfigs(ctx context.Context, sourceChannelID string) (main sdk.SessionConfig, mainKeys *sdk.KeyPool, sub sdk.SessionConfig, subKeys *sdk.KeyPool, err error) {
	if h.ResolveSession == nil {
		return sdk.SessionConfig{}, nil, sdk.SessionConfig{}, nil, fmt.Errorf("session manager is not configured")
	}
	if err := ctx.Err(); err != nil {
		return sdk.SessionConfig{}, nil, sdk.SessionConfig{}, nil, err
	}
	source, err := h.ResolveSession(ctx, sdk.Input{SessionID: h.sessionIDFor(sourceChannelID)})
	if err != nil {
		return sdk.SessionConfig{}, nil, sdk.SessionConfig{}, nil, err
	}
	main = source.Config()
	if strings.TrimSpace(string(main.Provider)) == "" || strings.TrimSpace(main.Model) == "" {
		return sdk.SessionConfig{}, nil, sdk.SessionConfig{}, nil, fmt.Errorf("this channel has no model settings yet; configure them with /model first")
	}
	mainKeys = h.ProviderKeys[main.Provider]
	if mainKeys == nil {
		return sdk.SessionConfig{}, nil, sdk.SessionConfig{}, nil, fmt.Errorf("provider %q has no API key pool", main.Provider)
	}
	subSession, err := h.ResolveSession(ctx, sdk.Input{SessionID: h.subSessionIDFor(sourceChannelID)})
	if err != nil {
		return sdk.SessionConfig{}, nil, sdk.SessionConfig{}, nil, err
	}
	sub = subSession.Config()
	if sub.Provider != "" {
		subKeys = h.ProviderKeys[sub.Provider]
		if subKeys == nil {
			return sdk.SessionConfig{}, nil, sdk.SessionConfig{}, nil, fmt.Errorf("provider %q has no API key pool", sub.Provider)
		}
	}
	return main, mainKeys, sub, subKeys, nil
}

// subSessionIDFor derives the channel's sub-agent session ID.
func (h *NewChannelHandler) subSessionIDFor(channelID string) string {
	return h.sessionIDFor(channelID) + ":sub"
}

// applySettings copies both validated source sessions into the new channel's
// sessions. The targets start fresh in main mode, so the plain setters land
// on the right side; the mode is copied last. A sub-active source with an
// empty sub side seeds the target sub from the target main (CHANGE-021).
func (h *NewChannelHandler) applySettings(ctx context.Context, targetSessionID string, main sdk.SessionConfig, mainKeys *sdk.KeyPool, sub sdk.SessionConfig, subKeys *sdk.KeyPool) (sdk.SessionConfig, sdk.SessionConfig, error) {
	target, err := h.ResolveSession(ctx, sdk.Input{SessionID: targetSessionID})
	if err != nil {
		return sdk.SessionConfig{}, sdk.SessionConfig{}, err
	}
	if err := copySideSettings(target, main, mainKeys); err != nil {
		return sdk.SessionConfig{}, sdk.SessionConfig{}, err
	}
	if main.Workspace != "" {
		if err := target.SetWorkspace(main.Workspace); err != nil {
			return sdk.SessionConfig{}, sdk.SessionConfig{}, err
		}
	}
	targetSub, err := h.ResolveSession(ctx, sdk.Input{SessionID: targetSessionID + ":sub"})
	if err != nil {
		return sdk.SessionConfig{}, sdk.SessionConfig{}, err
	}
	sourceWorkspace := sub.Workspace
	if sourceWorkspace == "" {
		sourceWorkspace = main.Workspace
	}
	if sourceWorkspace != "" {
		if err := targetSub.SetWorkspace(sourceWorkspace); err != nil {
			return sdk.SessionConfig{}, sdk.SessionConfig{}, err
		}
	}
	if sub.Provider != "" {
		if err := copySideSettings(targetSub, sub, subKeys); err != nil {
			return sdk.SessionConfig{}, sdk.SessionConfig{}, err
		}
		if err := targetSub.SetAgentMode(sdk.AgentModeSub); err != nil {
			return sdk.SessionConfig{}, sdk.SessionConfig{}, err
		}
	}
	if err := target.SetAgentMode(panelAgentMode(main)); err != nil {
		return sdk.SessionConfig{}, sdk.SessionConfig{}, err
	}
	if target.Config().AgentMode == sdk.AgentModeSub && targetSub.Config().Provider == "" {
		if err := copySideSettings(targetSub, target.Config(), mainKeys); err != nil {
			return sdk.SessionConfig{}, sdk.SessionConfig{}, err
		}
		if err := targetSub.SetAgentMode(sdk.AgentModeSub); err != nil {
			return sdk.SessionConfig{}, sdk.SessionConfig{}, err
		}
	}
	return target.Config(), targetSub.Config(), nil
}

// copySideSettings applies one side's settings onto a fresh session.
// SetProvider runs first because it resets the model and key index it then
// re-applies.
func copySideSettings(target *sdk.Session, config sdk.SessionConfig, keys *sdk.KeyPool) error {
	if err := target.SetProvider(config.Provider, keys); err != nil {
		return err
	}
	if err := target.SetModel(config.Model); err != nil {
		return err
	}
	if config.Temperature == nil {
		if err := target.ClearTemperature(); err != nil {
			return err
		}
	} else if err := target.SetTemperature(*config.Temperature); err != nil {
		return err
	}
	if config.ThinkingLevel == "" {
		if err := target.ClearThinkingLevel(); err != nil {
			return err
		}
	} else if err := target.SetThinkingLevel(config.ThinkingLevel); err != nil {
		return err
	}
	return target.SetKeyIndex(config.KeyIndex)
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
	main, mainKeys, sub, subKeys, err := h.sourceConfigs(context.Background(), sourceChannelID)
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
	appliedMain, appliedSub, err := h.applySettings(context.Background(), h.sessionIDFor(channel.ID), main, mainKeys, sub, subKeys)
	if err != nil {
		return followupEphemeral(s, i, "Channel created, but settings were not copied: "+err.Error())
	}
	if _, err := s.ChannelMessageSend(channel.ID, sessionSettingsSummary(appliedMain, appliedSub)); err != nil {
		return followupEphemeral(s, i, fmt.Sprintf("Channel created as <#%s> with the same model settings, but the summary message could not be delivered.", channel.ID))
	}
	return followupEphemeral(s, i, fmt.Sprintf("Created <#%s> with the same model settings as this channel.", channel.ID))
}
