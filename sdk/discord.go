package sdk

import (
	"context"
	"errors"
)

// DiscordInputMessage is the small transport DTO required by the Harness.
// A Discord library such as discordgo/disgo can populate it from its gateway event.
type DiscordInputMessage struct {
	SessionID  string
	ChannelID  string
	MessageID  string
	AuthorID   string
	AuthorName string
	Content    string
}

func DiscordToInput(message DiscordInputMessage) Input {
	return Input{
		Source:    "discord",
		SessionID: message.SessionID,
		Turn: Turn{
			Role: RoleUser,
			Content: []ContentPart{{Type: ContentText, Text: message.Content}},
		},
		Metadata: map[string]string{
			"channel_id":  message.ChannelID,
			"message_id":  message.MessageID,
			"author_id":   message.AuthorID,
			"author_name": message.AuthorName,
		},
	}
}

// DiscordInputSource adapts an existing Discord event channel. The actual
// Discord Gateway client stays outside the Harness core.
type DiscordInputSource struct {
	Messages <-chan DiscordInputMessage
}

func (s DiscordInputSource) Receive(ctx context.Context) (<-chan Input, error) {
	if s.Messages == nil {
		return nil, errors.New("sdk: discord input source has no message channel")
	}
	out := make(chan Input)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case message, ok := <-s.Messages:
				if !ok {
					return
				}
				select {
				case out <- DiscordToInput(message):
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

type DiscordSender interface {
	SendMessage(context.Context, string, string) error
}

type DiscordDisplay struct {
	Sender DiscordSender
}

func (d DiscordDisplay) Display(ctx context.Context, output Output) error {
	if d.Sender == nil {
		return errors.New("sdk: discord display has no sender")
	}
	channelID := output.Metadata["channel_id"]
	if channelID == "" {
		return errors.New("sdk: discord output has no channel_id")
	}
	text := ""
	for _, part := range output.Content {
		if part.Type == ContentText {
			text += part.Text
		}
	}
	if text == "" {
		return nil
	}
	return d.Sender.SendMessage(ctx, channelID, text)
}
