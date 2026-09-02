package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Tulipskun/ai/sdk"
)

type InputMessage struct {
	SessionID  string
	ChannelID  string
	MessageID  string
	AuthorID   string
	AuthorName string
	Content    string
}

func ToInput(message InputMessage) sdk.Input {
	return sdk.Input{
		Source:    "discord",
		SessionID: message.SessionID,
		Turn: sdk.Turn{
			Role: sdk.RoleUser,
			Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: message.Content}},
		},
		Metadata: map[string]string{
			"channel_id":  message.ChannelID,
			"message_id":  message.MessageID,
			"author_id":   message.AuthorID,
			"author_name": message.AuthorName,
		},
	}
}

type InputSource struct {
	Messages <-chan InputMessage
}

func (s InputSource) Receive(ctx context.Context) (<-chan sdk.Input, error) {
	if s.Messages == nil {
		return nil, errors.New("discord: input source has no message channel")
	}
	out := make(chan sdk.Input)
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
				case out <- ToInput(message):
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

type Sender interface {
	SendMessage(context.Context, string, string) error
}

type Display struct {
	Sender Sender
}

func (d Display) Source() string { return "discord" }

func (d Display) Display(ctx context.Context, output sdk.Output) error {
	if d.Sender == nil {
		return errors.New("discord: display has no sender")
	}
	channelID := output.Metadata["channel_id"]
	if channelID == "" {
		return errors.New("discord: output has no channel_id")
	}
	if output.Trace != nil {
		return d.displayTrace(ctx, channelID, *output.Trace)
	}
	var b strings.Builder
	for _, part := range output.Content {
		if part.Type == sdk.ContentText {
			b.WriteString(part.Text)
		}
	}
	text := b.String()
	if text == "" {
		return nil
	}
	return d.Sender.SendMessage(ctx, channelID, text)
}

func (d Display) displayTrace(ctx context.Context, channelID string, trace sdk.TraceEvent) error {
	payload := any(nil)
	switch trace.Stage {
	case sdk.TraceRequest:
		payload = sanitizeRequest(trace.Request)
	case sdk.TraceResponse:
		payload = sanitizeResponse(trace.Response)
	case sdk.TraceToolCall:
		payload = trace.ToolCall
	case sdk.TraceToolResult:
		payload = trace.ToolResult
	case sdk.TraceError:
		payload = map[string]string{"error": errorString(trace.Err)}
	default:
		payload = trace
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	text := fmt.Sprintf("[AI %s]\n```json\n%s\n```", trace.Stage, data)
	for _, chunk := range discordChunks(text, 1900) {
		if err := d.Sender.SendMessage(ctx, channelID, chunk); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeRequest(in *sdk.Request) any {
	if in == nil {
		return nil
	}
	copy := *in
	copy.Messages = append([]sdk.Turn(nil), in.Messages...)
	for i := range copy.Messages {
		if copy.Messages[i].Reasoning != nil {
			r := *copy.Messages[i].Reasoning
			if r.Text != "" {
				r.Text = "[redacted]"
			}
			copy.Messages[i].Reasoning = &r
		}
	}
	return &copy
}

func sanitizeResponse(in *sdk.Response) any {
	if in == nil {
		return nil
	}
	copy := *in
	if copy.Reasoning != nil {
		r := *copy.Reasoning
		if r.Text != "" {
			r.Text = "[redacted]"
		}
		copy.Reasoning = &r
	}
	return &copy
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func discordChunks(text string, max int) []string {
	if len(text) <= max {
		return []string{text}
	}
	chunks := make([]string, 0, (len(text)+max-1)/max)
	for len(text) > max {
		cut := strings.LastIndexByte(text[:max], '\n')
		if cut <= 0 {
			cut = max
		}
		chunks = append(chunks, text[:cut])
		text = strings.TrimLeft(text[cut:], "\n")
	}
	if text != "" {
		chunks = append(chunks, text)
	}
	return chunks
}
