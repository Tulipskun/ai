package discord

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Tulipskun/ai/sdk"
	"github.com/bwmarrin/discordgo"
)

// InputMessage is one normalised inbound Discord message. Attachments carries
// what the gateway was told about, Stored carries what actually reached the file
// store, and Problems carries a safe note for each file that did not (REQ-025).
// The last two are filled by Attachments.Hydrate before the message is
// converted, so a transport that never stores files leaves them empty.
type InputMessage struct {
	SessionID   string
	ChannelID   string
	MessageID   string
	AuthorID    string
	AuthorName  string
	Content     string
	Attachments []InputAttachment
	Stored      []StoredAttachment
	Problems    []AttachmentProblem

	// hydrated records that a pipeline has already resolved this message's
	// files, so a second consumer cannot download them again or replace real
	// references with "not stored" notes. The gateway pump sets it, and
	// Attachments.Hydrate is the only other writer.
	hydrated bool
}

// ToInput converts a hydrated message into the canonical Harness input.
//
// The Turn contract is deliberately untouched: a Discord file becomes an
// opaque reference in Input.Metadata plus a one-line reference in the text,
// never a new ContentPart type and never bytes, so no provider sees MIME data
// and the session history stays bounded (REQ-025, REQ-016, CON-003). The four
// original keys stay exactly as they were, because lifecycle continuation and
// reply routing depend on them (REQ-021).
//
// Inbound metadata schema (all values are strings; index suffixes start at 1 in
// delivery order):
//
//	attachment_count             stored references, = len(attachment_ids)
//	attachment_ids               store reference IDs, comma joined
//	attachment_<i>_id            one store reference ID (what read_attachment takes)
//	attachment_<i>_name          display filename, sanitised
//	attachment_<i>_content_type  type detected from magic bytes, else declared
//	attachment_<i>_size          bytes
//	attachment_<i>_path          relative path inside the attachment store
//	attachment_problem_count     files that were not stored
//	attachment_problems          sanitised names of those files, comma joined
//
// The per-attachment keys are the authoritative record: attachment_ids is a
// convenience join of the same values. A message with no files gains no keys,
// so a plain Discord turn keeps the exact map it had before attachments
// existed.
func ToInput(message InputMessage) sdk.Input {
	metadata := map[string]string{"channel_id": message.ChannelID, "message_id": message.MessageID, "author_id": message.AuthorID, "author_name": message.AuthorName}
	if count := len(message.Stored); count > 0 {
		metadata[MetaAttachmentCount] = strconv.Itoa(count)
		ids := make([]string, 0, count)
		for index, file := range message.Stored {
			ids = append(ids, file.RefID)
			metadata[AttachmentKey(index, MetaAttachmentID)] = file.RefID
			metadata[AttachmentKey(index, MetaAttachmentName)] = file.Name
			metadata[AttachmentKey(index, MetaAttachmentContentType)] = file.ContentType
			metadata[AttachmentKey(index, MetaAttachmentSize)] = strconv.FormatInt(file.Size, 10)
			metadata[AttachmentKey(index, MetaAttachmentPath)] = file.Path
		}
		metadata[MetaAttachmentIDs] = strings.Join(ids, ",")
	}
	if count := len(message.Problems); count > 0 {
		metadata[MetaAttachmentProblemCount] = strconv.Itoa(count)
		names := make([]string, 0, count)
		for _, problem := range message.Problems {
			names = append(names, problem.Name)
		}
		metadata[MetaAttachmentProblems] = strings.Join(names, ",")
	}
	return sdk.Input{Source: "discord", SessionID: message.SessionID, Turn: sdk.Turn{Role: sdk.RoleUser, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: message.Content}}}, Metadata: metadata}
}

// InputSource turns the gateway's message channel into canonical Harness
// inputs. Attachments is optional: when it is set, every message that carries
// files is hydrated — the files are downloaded into the session's store — before
// the input is emitted, so the reference metadata and the reference text are
// already resolved when the turn starts.
//
// Hydration happens in this consumer goroutine rather than in the gateway event
// handler on purpose: a download must never block the Discord read loop, and a
// message that is queued behind a slow intake still reaches the harness intact
// instead of being dropped.
type InputSource struct {
	Messages    <-chan InputMessage
	Attachments *Attachments
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
				message = s.Attachments.Hydrate(ctx, message)
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

// Display renders Harness outputs to one Discord channel. Attachments is
// optional: a nil value (the zero state every pre-attachment caller already
// used) disables outbound uploads and reports the request instead of dropping it
// silently.
type Display struct {
	Sender      Sender
	Attachments *Attachments
}

// attachments returns the configured pipeline or an inert one, so callers never
// need a nil check.
func (d Display) attachments() *Attachments {
	if d.Attachments != nil {
		return d.Attachments
	}
	return &Attachments{}
}

func (d Display) Source() string { return "discord" }
func (d Display) Display(ctx context.Context, output sdk.Output) error {
	if d.Sender == nil {
		return errors.New("discord: display has no sender")
	}
	channelID := output.Metadata["channel_id"]
	if gateway, ok := d.Sender.(*Gateway); ok && output.Metadata["trace_actor"] != "" {
		if channelID == "" {
			return errors.New("discord: output has no channel_id")
		}
		return errors.Join(gateway.displayActorOutput(ctx, output), d.sendOutbound(ctx, channelID, output))
	}
	if channelID == "" {
		return errors.New("discord: output has no channel_id")
	}
	if output.Trace != nil {
		return errors.Join(d.displayTrace(ctx, channelID, *output.Trace), d.sendOutbound(ctx, channelID, output))
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
	return d.sendOutbound(ctx, channelID, output)
}

// sendOutbound uploads the files an output asks for, after its text has been
// delivered. It is a no-op for the overwhelming majority of turns: only output
// metadata that names attachment references triggers an upload, so the plain
// text path, its chunking, and its error behaviour are unchanged (REQ-002).
//
// Uploads run on a context derived with context.WithoutCancel plus the
// transport's own upload timeout. The harness display timeout bounds a text
// reply, and it is far too short for a design file; keeping the budget here
// means a large upload can finish without lengthening the text-reply timeout for
// the whole system. A cancelled shutdown still cannot half-send a file, because
// the timeout is what bounds the operation.
func (d Display) sendOutbound(ctx context.Context, channelID string, output sdk.Output) error {
	request, requested := outboundRequested(output.Metadata)
	if !requested {
		return nil
	}
	attachments := d.attachments()
	sender, ok := d.Sender.(fileSender)
	if !ok {
		// A sender without upload capability must say so rather than drop the
		// files silently (REQ-022).
		return d.sendOutboundNotes(ctx, channelID, []string{outboundUnsupportedNote})
	}
	if !attachments.markSent(channelID, sentKey(request)) {
		return nil
	}
	uploadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), attachments.uploadTimeout())
	defer cancel()
	files, notes := attachments.resolveOutbound(uploadCtx, output.SessionID, request)
	defer func() {
		for _, file := range files {
			file.close()
		}
	}()
	// Text-first, files-next is deliberate: the readable answer must not be lost
	// because an upload failed.
	if len(files) == 0 {
		if len(notes) == 0 {
			return nil
		}
		return d.sendOutboundNotes(ctx, channelID, notes)
	}
	discordFiles := make([]*discordgo.File, 0, len(files))
	for _, file := range files {
		discordFiles = append(discordFiles, file.file)
	}
	if len(notes) > 0 {
		notes = append(notes, fmt.Sprintf("📎 %d file(s) attached", len(discordFiles)))
	}
	content := strings.Join(notes, "\n")
	if err := sender.SendFiles(uploadCtx, channelID, content, discordFiles); err != nil {
		// One safe indicator, never the raw error text: a REST failure can carry
		// a URL or a response body (REQ-022).
		if sendErr := d.sendOutboundNotes(ctx, channelID, []string{outboundSendFailureNote}); sendErr != nil {
			return errors.Join(err, sendErr)
		}
		return err
	}
	return nil
}

func (d Display) sendOutboundNotes(ctx context.Context, channelID string, notes []string) error {
	text := strings.Join(notes, "\n")
	if text == "" {
		return nil
	}
	for _, chunk := range discordChunks(truncateText(text, maxEmbedChars), 1900) {
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
		if !hasTrace {
			return nil
		}
		if footer != nil {
			footer.startTurnFooter(channelID)
		}
		return sender.updateToolTrace(ctx, channelID, message, pendingRequestLine)
	case sdk.TraceProviderReady:
		if !hasTrace {
			return nil
		}
		return sender.updateToolTrace(ctx, channelID, withElapsed(message, trace.Elapsed), pendingRequestLine)
	case sdk.TraceResponseText:
		return nil
	case sdk.TraceResponseContent:
		if trace.Response != nil && footer != nil {
			footer.updateTurnFooterUsage(channelID, trace.Response.Usage)
		}
		text := trace.Text
		if text == "" {
			text = responseContent(trace.Response)
		}
		if text == "" {
			return nil
		}
		if textSender, ok := d.Sender.(textTraceSender); ok {
			return textSender.appendTextTrace(ctx, channelID, text)
		}
		for _, chunk := range discordChunks(text, 1900) {
			if err := d.Sender.SendMessage(ctx, channelID, chunk); err != nil {
				return err
			}
		}
		return nil
	case sdk.TraceToolCall:
		if trace.ToolCall == nil || !hasTrace || formatToolTraceCall(trace.ToolCall) == "" {
			return nil
		}
		return sender.appendToolTrace(ctx, channelID, withElapsed(formatToolTraceCall(trace.ToolCall), trace.Elapsed))
	case sdk.TraceToolRunning:
		return nil
	case sdk.TraceToolResult:
		if !hasTrace || trace.ToolResult == nil || formatToolTraceCall(trace.ToolCall) == "" {
			return nil
		}
		return sender.updateToolTrace(ctx, channelID, withElapsed(formatToolResult(trace.ToolCall, trace.ToolResult), trace.Elapsed), toolLineFor(trace.ToolCall))
	case sdk.TraceResponse:
		if trace.Response != nil && footer != nil {
			footer.updateTurnFooterUsage(channelID, trace.Response.Usage)
		}
		return d.finishTrace(ctx, channelID)
	case sdk.TraceRetryWait:
		text := "Retrying request"
		if trace.Err != nil {
			text += " (" + safeErrorSummary(trace.Err) + ")"
		}
		if trace.RetryAfter > 0 {
			text += " in " + formatDuration(trace.RetryAfter)
		}
		if status, ok := d.Sender.(retryStatusManager); ok {
			return status.updateRetryStatus(ctx, channelID, text)
		}
		if hasTrace {
			return sender.updateToolTrace(ctx, channelID, text, pendingRequestLine)
		}
		return d.Sender.SendMessage(ctx, channelID, text)
	case sdk.TraceError:
		text := "❌ Request failed: " + safeErrorSummary(trace.Err)
		var err error
		if hasTrace {
			err = sender.appendToolTrace(ctx, channelID, withElapsed(text, trace.Elapsed))
		} else {
			err = d.Sender.SendMessage(ctx, channelID, text)
		}
		return errors.Join(err, d.finishTrace(ctx, channelID))
	default:
		return nil
	}
}

const (
	maxToolTraceLength = 500
	maxTextTraceLength = 4000

	// Safe outbound indicators. REQ-022 forbids leaking raw tool output or
	// network error text into a channel, so these are the only strings the
	// upload path may show, and none of them names a path, a URL, or another
	// session.
	outboundUnsupportedNote = "⚠️ no file was sent; this Discord sender cannot upload files"
	outboundSendFailureNote = "❌ the file upload failed; please try again"
)

// Outbound metadata schema (written by a caller into sdk.Output.Metadata, read
// only inside this transport):
//
//	out_attachment_ids   comma-separated filestore reference IDs to upload
//	out_attachments      "manifest" (everything the session's store holds) or
//	                     "latest" (the newest one); ignored when IDs are named
//	out_attachment_raw   refused by design: inline bytes/base64 are never sent,
//	                     and the refusal is reported instead of swallowed
//
// The store manifest of output.SessionID is the fallback source because the
// canonical path cannot carry a file; it stays scoped to that one session, so no
// other session's attachment can be named into a channel.

func withElapsed(text string, elapsed time.Duration) string {
	if elapsed <= 0 {
		return text
	}
	return text + " · " + formatDuration(elapsed)
}
func formatCount(n int) string {
	if n < 0 {
		n = 0
	}
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return strconv.Itoa(n)
}
func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Round(time.Second).Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	s %= 60
	if m < 60 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	h := m / 60
	m %= 60
	return fmt.Sprintf("%dh %dm", h, m)
}
func truncateText(text string, max int) string {
	if max <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max-1]) + "…"
}
func formatToolTraceCall(call *sdk.ToolCall) string {
	if call == nil {
		return "Working"
	}
	if strings.TrimSpace(call.Name) == "" {
		return "Working"
	}
	return truncateOneLine(strings.ReplaceAll(call.Name, "_", " "), 80)
}
func formatToolCall(call *sdk.ToolCall) string { return formatToolTraceCall(call) }
func formatToolResult(call *sdk.ToolCall, result *sdk.ToolResult) string {
	if result == nil {
		return ""
	}
	prefix := "✅ "
	if result.IsError {
		prefix = "❌ "
	}
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
	if max <= 0 || len([]rune(text)) <= max {
		return text
	}
	runes := []rune(text)
	return string(runes[:max-1]) + "…"
}
func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(time.Second).String()
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
func safeErrorSummary(err error) string {
	if err == nil {
		return "please try again"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "request timed out"
	}
	var status interface{ HTTPStatusCode() int }
	if errors.As(err, &status) {
		switch code := status.HTTPStatusCode(); code {
		case 401, 403:
			return fmt.Sprintf("authorization failed (HTTP %d)", code)
		case 429:
			return "rate limited (HTTP 429)"
		default:
			if code >= 400 && code <= 599 {
				return fmt.Sprintf("service error (HTTP %d)", code)
			}
		}
	}
	return "service unavailable; please try again"
}
func (d Display) finishTrace(ctx context.Context, channelID string) error {
	if footer, ok := d.Sender.(turnFooterTracker); ok {
		footer.stopTurnFooter(channelID)
	}
	var flushErr, clearErr error
	if sender, ok := d.Sender.(toolTraceSender); ok {
		flushErr = sender.flushToolTrace(ctx, channelID)
	}
	if status, ok := d.Sender.(retryStatusManager); ok {
		clearErr = status.clearRetryStatus(ctx, channelID)
	}
	return errors.Join(flushErr, clearErr)
}
