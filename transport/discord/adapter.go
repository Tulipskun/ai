package discord

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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
	return sdk.Input{Source: "discord", SessionID: message.SessionID, Turn: sdk.Turn{Role: sdk.RoleUser, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: message.Content}}}, Metadata: map[string]string{"channel_id": message.ChannelID, "message_id": message.MessageID, "author_id": message.AuthorID, "author_name": message.AuthorName}}
}

type InputSource struct{ Messages <-chan InputMessage }

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
type toolTraceSender interface {
	setToolTrace(context.Context, string, []string) error
	appendToolTrace(context.Context, string, string) error
	updateToolTrace(context.Context, string, string, func(string) bool) error
	flushToolTrace(context.Context, string) error
	clearToolTrace(context.Context, string) error
}
type textTraceSender interface {
	appendTextTrace(context.Context, string, string) error
}
type turnFooterTracker interface {
	startTurnFooter(string)
	updateTurnFooterUsage(string, sdk.Usage)
	stopTurnFooter(string)
}
type retryStatusManager interface {
	RetryStatusSender
	updateRetryStatus(context.Context, string, string) error
	clearRetryStatus(context.Context, string) error
}
type Display struct{ Sender Sender }

func (d Display) Source() string { return "discord" }
func (d Display) Display(ctx context.Context, output sdk.Output) error {
	if d.Sender == nil {
		return errors.New("discord: display has no sender")
	}
	if gateway, ok := d.Sender.(*Gateway); ok {
		return gateway.displayActorOutput(ctx, output)
	}
	channelID := output.Metadata["channel_id"]
	if channelID == "" {
		return errors.New("discord: output has no channel_id")
	}
	if output.Trace != nil {
		return d.displayTrace(ctx, channelID, *output.Trace)
	}
	text := responseContent(&sdk.Response{Content: output.Content})
	if text == "" {
		text = responseContent(&output.Response)
	}
	for _, chunk := range discordChunks(text, 1900) {
		if err := d.Sender.SendMessage(ctx, channelID, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (d Display) displayTrace(ctx context.Context, channelID string, trace sdk.TraceEvent) error {
	sender, hasTrace := d.Sender.(toolTraceSender)
	footer, _ := d.Sender.(turnFooterTracker)
	message := sdk.TraceMessage(sdk.TraceEvent{Stage: trace.Stage})
	switch trace.Stage {
	case sdk.TraceRequest:
		if !hasTrace { return nil }
		if footer != nil { footer.startTurnFooter(channelID) }
		return sender.updateToolTrace(ctx, channelID, message, pendingRequestLine)
	case sdk.TraceProviderReady:
		if !hasTrace { return nil }
		return sender.updateToolTrace(ctx, channelID, withElapsed(message, trace.Elapsed), pendingRequestLine)
	case sdk.TraceResponseText:
		return nil
	case sdk.TraceResponseContent:
		if trace.Response != nil && footer != nil { footer.updateTurnFooterUsage(channelID, trace.Response.Usage) }
		text := trace.Text
		if text == "" { text = responseContent(trace.Response) }
		if text == "" { return nil }
		if textSender, ok := d.Sender.(textTraceSender); ok { return textSender.appendTextTrace(ctx, channelID, text) }
		for _, chunk := range discordChunks(text, 1900) {
			if err := d.Sender.SendMessage(ctx, channelID, chunk); err != nil { return err }
		}
		return nil
	case sdk.TraceToolCall:
		if trace.ToolCall == nil || !hasTrace || formatToolTraceCall(trace.ToolCall) == "" { return nil }
		return sender.appendToolTrace(ctx, channelID, withElapsed(formatToolTraceCall(trace.ToolCall), trace.Elapsed))
	case sdk.TraceToolRunning:
		return nil
	case sdk.TraceToolResult:
		if !hasTrace || trace.ToolResult == nil || formatToolTraceCall(trace.ToolCall) == "" { return nil }
		return sender.updateToolTrace(ctx, channelID, withElapsed(formatToolResult(trace.ToolCall, trace.ToolResult), trace.Elapsed), toolLineFor(trace.ToolCall))
	case sdk.TraceResponse:
		if trace.Response != nil && footer != nil { footer.updateTurnFooterUsage(channelID, trace.Response.Usage) }
		return d.finishTrace(ctx, channelID)
	case sdk.TraceRetryWait:
		text := "Retrying request"
		if trace.Err != nil { text += " (" + safeErrorSummary(trace.Err) + ")" }
		if trace.RetryAfter > 0 { text += " in " + formatDuration(trace.RetryAfter) }
		if status, ok := d.Sender.(retryStatusManager); ok { return status.updateRetryStatus(ctx, channelID, text) }
		if hasTrace { return sender.updateToolTrace(ctx, channelID, text, pendingRequestLine) }
		return d.Sender.SendMessage(ctx, channelID, text)
	case sdk.TraceError:
		text := "❌ Request failed: " + safeErrorSummary(trace.Err)
		var err error
		if hasTrace { err = sender.appendToolTrace(ctx, channelID, withElapsed(text, trace.Elapsed)) } else { err = d.Sender.SendMessage(ctx, channelID, text) }
		return errors.Join(err, d.finishTrace(ctx, channelID))
	default:
		return nil
	}
}

const maxToolTraceLength = 500
const maxTextTraceLength = 4000

func withElapsed(text string, elapsed time.Duration) string {
	if elapsed <= 0 { return text }
	return text + " · " + formatDuration(elapsed)
}
func formatCount(n int) string {
	if n < 0 { n = 0 }
	if n >= 1000000 { return fmt.Sprintf("%.1fM", float64(n)/1000000) }
	if n >= 1000 { return fmt.Sprintf("%dk", n/1000) }
	return strconv.Itoa(n)
}
func formatElapsed(d time.Duration) string {
	if d < 0 { d = 0 }
	s := int(d.Round(time.Second).Seconds())
	if s < 60 { return fmt.Sprintf("%ds", s) }
	m := s / 60
	s %= 60
	if m < 60 { return fmt.Sprintf("%dm %ds", m, s) }
	h := m / 60
	m %= 60
	return fmt.Sprintf("%dh %dm", h, m)
}
func truncateText(text string, max int) string {
	if max <= 0 { return text }
	runes := []rune(text)
	if len(runes) <= max { return text }
	return string(runes[:max-1]) + "…"
}
func formatToolTraceCall(call *sdk.ToolCall) string {
	if call == nil { return "Working" }
	switch call.Name {
	case "plan", "update_plan", "subagent_status", "subagent_history", "accept_subagent_result":
		return ""
	case "delegate_to_subagent":
		return "Working on your task"
	case "follow_up_subagent":
		return "Retrying task"
	case "stop_subagent":
		return "Stopping task"
	default:
		return truncateOneLine(strings.ReplaceAll(call.Name, "_", " "), 80)
	}
}
func formatToolCall(call *sdk.ToolCall) string { return formatToolTraceCall(call) }
func formatToolResult(call *sdk.ToolCall, result *sdk.ToolResult) string {
	if result == nil { return "" }
	prefix := "✅ "
	if result.IsError { prefix = "❌ " }
	return truncateOneLine(prefix+formatToolTraceCall(call), maxToolTraceLength)
}
var pendingRequestText = sdk.TraceMessage(sdk.TraceEvent{Stage: sdk.TraceRequest})
func pendingRequestLine(item string) bool { return item == pendingRequestText }
func toolLineFor(call *sdk.ToolCall) func(string) bool {
	prefix := formatToolTraceCall(call)
	return func(item string) bool { return item == prefix || strings.HasPrefix(item, prefix+" · ") }
}
func truncateOneLine(text string, max int) string {
	text = oneLine(text)
	if max <= 0 || len([]rune(text)) <= max { return text }
	runes := []rune(text)
	return string(runes[:max-1]) + "…"
}
func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }
func formatDuration(d time.Duration) string {
	if d < time.Second { return d.Round(time.Millisecond).String() }
	return d.Round(time.Second).String()
}
func responseContent(response *sdk.Response) string {
	if response == nil { return "" }
	var b strings.Builder
	for _, part := range response.Content {
		if part.Type == sdk.ContentText { b.WriteString(part.Text) }
	}
	return b.String()
}
func safeErrorSummary(err error) string {
	if err == nil { return "please try again" }
	if errors.Is(err, context.Canceled) { return "cancelled" }
	if errors.Is(err, context.DeadlineExceeded) { return "request timed out" }
	var status interface{ HTTPStatusCode() int }
	if errors.As(err, &status) {
		switch code := status.HTTPStatusCode(); code {
		case 401, 403: return fmt.Sprintf("authorization failed (HTTP %d)", code)
		case 429: return "rate limited (HTTP 429)"
		default:
			if code >= 400 && code <= 599 { return fmt.Sprintf("service error (HTTP %d)", code) }
		}
	}
	return "service unavailable; please try again"
}
func (d Display) finishTrace(ctx context.Context, channelID string) error {
	if footer, ok := d.Sender.(turnFooterTracker); ok { footer.stopTurnFooter(channelID) }
	var flushErr, clearErr error
	if sender, ok := d.Sender.(toolTraceSender); ok { flushErr = sender.flushToolTrace(ctx, channelID) }
	if status, ok := d.Sender.(retryStatusManager); ok { clearErr = status.clearRetryStatus(ctx, channelID) }
	return errors.Join(flushErr, clearErr)
}
