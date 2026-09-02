package discord

import (
	"context"
	"errors"
	"strings"

	"github.com/Tulipskun/ai/sdk"
)

const Source = "discord"

type InputMessage struct {
	SessionID  string
	ChannelID  string
	MessageID  string
	AuthorID   string
	AuthorName string
	Content    string
}

func ToInput(message InputMessage) sdk.Input {
	return sdk.Input{Source: Source, SessionID: message.SessionID, Turn: sdk.Turn{Role: sdk.RoleUser, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: message.Content}}}, Metadata: map[string]string{"channel_id": message.ChannelID, "message_id": message.MessageID, "author_id": message.AuthorID, "author_name": message.AuthorName}}
}

type InputSource struct{ Messages <-chan InputMessage }

func (s InputSource) Receive(ctx context.Context) (<-chan sdk.Input, error) {
	if s.Messages == nil { return nil, errors.New("discord: input source has no message channel") }
	out := make(chan sdk.Input)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done(): return
			case message, ok := <-s.Messages:
				if !ok { return }
				select { case out <- ToInput(message): case <-ctx.Done(): return }
			}
		}
	}()
	return out, nil
}

type Sender interface { SendMessage(context.Context, string, string) error }

type Display struct{ Sender Sender }
func (d Display) Source() string { return Source }
func (d Display) Display(ctx context.Context, output sdk.Output) error {
	if d.Sender == nil { return errors.New("discord: display has no sender") }
	if output.Source != Source { return nil }
	channelID := output.Metadata["channel_id"]
	if channelID == "" { return errors.New("discord: output has no channel_id") }
	var b strings.Builder
	for _, part := range output.Content { if part.Type == sdk.ContentText { b.WriteString(part.Text) } }
	if b.Len() == 0 { return nil }
	return d.Sender.SendMessage(ctx, channelID, b.String())
}
