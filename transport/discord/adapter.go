package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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

type RetryStatusSender interface {
	SendStatusMessage(context.Context, string, string) (string, error)
	EditMessage(context.Context, string, string, string) error
	DeleteMessage(context.Context, string, string) error
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
	var text string
	switch trace.Stage {
	case sdk.TraceToolCall:
		text = formatTraceJSON("[AI tool_call]", trace.ToolCall)
	case sdk.TraceToolResult:
		text = formatTraceJSON("[AI tool_result]", trace.ToolResult)
	case sdk.TraceResponse:
		text = responseContent(trace.Response)
		if status, ok := d.Sender.(RetryStatusSender); ok {
			if err := status.clearRetryStatus(ctx, channelID); err != nil {
				return err
			}
		}
	case sdk.TraceRetryWait:
		if trace.Err != nil && trace.RetryAfter > 0 {
			text = fmt.Sprintf("[AI retry] %s\nกำลังรอ %s ก่อน retry", trace.Err, formatDuration(trace.RetryAfter))
		} else if trace.Err != nil {
			text = fmt.Sprintf("[AI retry] %s\nกำลังรอก่อน retry", trace.Err)
		}
		if status, ok := d.Sender.(RetryStatusSender); ok {
			return status.updateRetryStatus(ctx, channelID, text)
		}
	default:
		return nil
	}
	if text == "" {
		return nil
	}
	for _, chunk := range discordChunks(text, 1900) {
		if err := d.Sender.SendMessage(ctx, channelID, chunk); err != nil {
			return err
		}
	}
	return nil
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}

func formatTraceJSON(label string, value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s\n```json\n%s\n```", label, data)
}

func responseContent(response *sdk.Response) string {
	if response == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range response.Content {
		if part.Type == sdk.ContentText {
			b.WriteString(part.Text)
		}
	}
	return b.String()
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
