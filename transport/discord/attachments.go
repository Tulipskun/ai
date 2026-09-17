package discord

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tulipskun/ai/runtime/filestore"
	"github.com/bwmarrin/discordgo"
)

// Attachment transport boundaries (REQ-025, REQ-026, CON-011).
//
// Inbound: Discord reports files on a message. The gateway downloads each one,
// stores it under the session that the message routes to, and hands the model an
// opaque reference (name, content type, size, relative path) through
// Input.Metadata only. The canonical Turn/ContentPart contract is untouched, so
// no provider ever sees MIME data or bytes (REQ-016, REQ-025).
//
// Outbound: when a display output carries file references, the transport
// resolves them from that store and uploads them with one multipart message.
// File content never travels through the canonical SDK text path, which keeps
// CON-004 true in both directions: the agent core stays transport-agnostic and
// the Discord module owns the wire format.
//
// The default budgets below are transport fallbacks. Production values come from
// config/attachment.json, resolved by cmd/ai (CON-001).

const (
	defaultDownloadTimeout = 30 * time.Second
	defaultUploadTimeout   = 2 * time.Minute
	defaultSendFileBytes   = int64(8) << 20
	defaultSendFileCount   = 10

	// maxReferenceRunes caps one token of a reference line. A filename comes
	// from Discord and is attacker-controlled, so it must not be able to pad a
	// turn or an embed.
	maxReferenceRunes = 120

	// MetaAttachmentCount and friends are the inbound metadata schema written by
	// ToInput; MetaOutAttachmentIDs and MetaOutAttachments are the outbound
	// schema this transport reads. The inbound keys are documented on ToInput.
	MetaAttachmentCount        = "attachment_count"
	MetaAttachmentIDs          = "attachment_ids"
	MetaAttachmentPrefix       = "attachment_"
	MetaAttachmentID           = "id"
	MetaAttachmentName         = "name"
	MetaAttachmentContentType  = "content_type"
	MetaAttachmentSize         = "size"
	MetaAttachmentPath         = "path"
	MetaAttachmentProblemCount = "attachment_problem_count"
	MetaAttachmentProblems     = "attachment_problems"

	// MetaOutAttachmentIDs is a comma-separated list of store reference IDs for
	// the session of the output. MetaOutAttachments accepts "manifest" (send
	// every attachment the session's store holds, oldest first) or "latest".
	// MetaOutAttachmentRaw is deliberately refused: it exists so the transport
	// can report that inline file content is unsupported instead of silently
	// ignoring the request. All three are read only by this transport; nothing
	// in the canonical path interprets them.
	MetaOutAttachmentIDs = "out_attachment_ids"
	MetaOutAttachments   = "out_attachments"
	MetaOutAttachmentRaw = "out_attachment_raw"

	// OutboundModeManifest and OutboundModeLatest are the accepted values of
	// MetaOutAttachments. They exist because the canonical path cannot carry a
	// file: a mode is the only way to ask for "whatever this session stored"
	// without naming an opaque ID that no caller outside the store knows.
	OutboundModeManifest = "manifest"
	OutboundModeLatest   = "latest"
)

// Outbound producer (implemented, see CHANGE-033): the worker tool
// `send_attachment` (tools/attachments.go) validates the opaque reference
// against the session store and records the intent via
// sdk.RecordOutboundAttachment (capped at MaxOutboundAttachmentIDs=10);
// HarnessLoop.Entry (sdk/loop.go) drains via TakeOutboundAttachmentIDs and
// stamps Output.Metadata[out_attachment_ids] (merged, per-turn dedup) for
// this transport's SendFiles path. File bytes never travel the SDK text path.

// AttachmentKey builds the indexed inbound key for one stored attachment, so a
// reader of the metadata does not have to spell the shape out.
func AttachmentKey(index int, field string) string {
	return MetaAttachmentPrefix + strconv.Itoa(index+1) + "_" + field
}

// InputAttachment is one file reported by Discord on an inbound message. The
// fields mirror discordgo.MessageAttachment, kept as a local copy so the rest
// of the transport does not depend on the wire struct. URL and ProxyURL are
// fetch locations, never display text: a CDN URL carries a signed token, so it
// must not reach metadata or message content.
type InputAttachment struct {
	// ID is Discord's attachment ID. It is diagnostic only; the reference the
	// agent sees is the store ID recorded in StoredAttachment.
	ID          string
	Name        string
	ContentType string
	Size        int64
	URL         string
	ProxyURL    string
}

// StoredAttachment is a downloaded attachment that now lives in the file store.
// It carries metadata only.
type StoredAttachment struct {
	RefID       string
	SourceID    string
	Name        string
	ContentType string
	Size        int64
	Path        string
}

// AttachmentProblem is a safe, human-readable note about one attachment that
// could not be stored. It names the file and a generic reason, never a URL, a
// response body, or raw error text from the network stack.
type AttachmentProblem struct {
	Name   string
	Reason string
	Size   int64
}

// AttachmentStore is the transport's view of the file store. The method set
// matches *filestore.Store, so the caller injects the concrete store without an
// adapter. filestore is a standard-library-only leaf package with no transport
// or provider knowledge, so naming its types here adds no import cycle and
// keeps CON-004 intact. The store owns session-key encoding, size limits,
// magic-byte detection, safePath/withinRoot checks, and the manifest, so this
// transport never builds a path.
type AttachmentStore interface {
	PutWithContentType(ctx context.Context, sessionKey, name, contentType string, r io.Reader) (filestore.Ref, error)
	Get(ctx context.Context, sessionKey, refID string) (io.ReadCloser, filestore.Meta, error)
	List(ctx context.Context, sessionKey string) ([]filestore.Meta, error)
	Limits() filestore.Limits
}

// Attachments configures both directions of the file boundary for one gateway.
// A nil store disables intake storage and outbound sending without failing the
// turn, which mirrors a disabled config/attachment.json.
type Attachments struct {
	Store AttachmentStore
	// Client downloads attachments. It is injectable so tests never touch the
	// network; a zero-valued timeout falls back to http.Client's own default.
	Client *http.Client
	// DownloadTimeout bounds one attachment fetch and UploadTimeout bounds one
	// multipart send. Both are per-operation budgets owned by the transport:
	// they are applied to a context derived with context.WithoutCancel, so the
	// 10s harness display timeout cannot kill an in-flight upload of a design
	// file, and so a cancelled shutdown cannot half-write a file.
	DownloadTimeout time.Duration
	UploadTimeout   time.Duration
	// MaxSendFileBytes and MaxSendFileCount bound one outbound message. Discord
	// rejects oversized uploads on its own terms, so refusing early keeps the
	// failure readable instead of opaque.
	MaxSendFileBytes int64
	MaxSendFileCount int

	mu   sync.Mutex
	sent map[string]map[string]bool
}

func (a *Attachments) client() *http.Client {
	if a == nil || a.Client == nil {
		return http.DefaultClient
	}
	return a.Client
}
func (a *Attachments) downloadTimeout() time.Duration {
	if a == nil || a.DownloadTimeout <= 0 {
		return defaultDownloadTimeout
	}
	return a.DownloadTimeout
}
func (a *Attachments) uploadTimeout() time.Duration {
	if a == nil || a.UploadTimeout <= 0 {
		return defaultUploadTimeout
	}
	return a.UploadTimeout
}
func (a *Attachments) sendFileBytes() int64 {
	if a == nil || a.MaxSendFileBytes <= 0 {
		return defaultSendFileBytes
	}
	return a.MaxSendFileBytes
}
func (a *Attachments) sendFileCount() int {
	if a == nil || a.MaxSendFileCount <= 0 {
		return defaultSendFileCount
	}
	return a.MaxSendFileCount
}

// enabled reports whether files can be stored and therefore referenced.
func (a *Attachments) enabled() bool { return a != nil && a.Store != nil }

// Hydrate downloads every attachment of one inbound message into the store of
// the session the message will be routed to, then fills in the resolved
// references and the safe problem notes. It never fails the message: a broken,
// oversized, or unreachable file leaves a text note behind instead of dropping
// the turn (REQ-025).
//
// The session key is message.SessionID exactly as the gateway resolved it, which
// is the identity the harness gives the session and the attachment tools read
// back, so a file fetched for a channel is readable by that channel's session
// and by no other. The store applies its own base64 session-key encoding; this
// transport never touches a path.
func (a *Attachments) Hydrate(ctx context.Context, message InputMessage) InputMessage {
	// Hydration is once-per-message. The gateway pump resolves files before it
	// forwards a message, and a second pass would either download the same file
	// twice into the store or — when a later consumer has no store — overwrite
	// real references with "not stored" notes.
	if len(message.Attachments) == 0 || message.hydrated {
		return message
	}
	// The flag is set on the value being returned, not through a deferred call:
	// message is a copy, so a deferred write would land on that copy after the
	// result has already been handed to the caller, and the next consumer in the
	// chain (the gateway pump hands over to InputSource) would hydrate the same
	// message a second time.
	message.hydrated = true
	stored := make([]StoredAttachment, 0, len(message.Attachments))
	problems := make([]AttachmentProblem, 0)
	for _, attachment := range message.Attachments {
		if !a.enabled() {
			problems = append(problems, AttachmentProblem{Name: displayName(attachment.Name), Reason: notStoredDisabled, Size: attachment.Size})
			continue
		}
		if err := ctx.Err(); err != nil {
			problems = append(problems, AttachmentProblem{Name: displayName(attachment.Name), Reason: "not stored; the turn was cancelled", Size: attachment.Size})
			continue
		}
		ref, reason, err := a.store(ctx, message.SessionID, attachment)
		if err != nil {
			problems = append(problems, AttachmentProblem{Name: displayName(attachment.Name), Reason: reason, Size: attachment.Size})
			continue
		}
		stored = append(stored, ref)
	}
	message.Stored = stored
	message.Problems = problems
	// An attachment-only message must still carry text, or the harness would see
	// an empty turn (REQ-025). The text is a reference — name, detected type,
	// size — never the file's content.
	if notes := AttachmentNotes(stored, problems); notes != "" {
		if strings.TrimSpace(message.Content) == "" {
			message.Content = notes
		} else {
			message.Content = message.Content + "\n" + notes
		}
	}
	return message
}

// Safe failure reasons. They are the only text a storage failure may produce, so
// no URL, no response body, and no filesystem path can reach the model or a
// channel (REQ-022).
const (
	notStoredDisabled = "not stored; attachment storage is disabled"
	notStoredSize     = "not stored; it is larger than the attachment size limit"
	notStoredNoURL    = "not stored; the message did not include a download location"
	notStoredBadURL   = "not stored; the download location was unusable"
	notStoredNetwork  = "not stored; it could not be downloaded"
	notStoredRejected = "not stored; the attachment store rejected it"
	notStoredCancel   = "not stored; the turn was cancelled"
)

// store fetches one attachment and writes it to the store. It returns the
// reference, or the safe reason a reader should show.
func (a *Attachments) store(ctx context.Context, sessionID string, attachment InputAttachment) (StoredAttachment, string, error) {
	limits := a.Store.Limits()
	if attachment.Size > limits.MaxFileBytes {
		return StoredAttachment{}, notStoredSize, fmt.Errorf("discord: attachment %q exceeds the %d byte limit", displayName(attachment.Name), limits.MaxFileBytes)
	}
	location := attachment.ProxyURL
	if strings.TrimSpace(location) == "" {
		location = attachment.URL
	}
	if strings.TrimSpace(location) == "" {
		return StoredAttachment{}, notStoredNoURL, errors.New("discord: attachment has no download location")
	}
	downloadCtx, cancel := context.WithTimeout(ctx, a.downloadTimeout())
	defer cancel()
	request, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, location, nil)
	if err != nil {
		return StoredAttachment{}, notStoredBadURL, err
	}
	response, err := a.client().Do(request)
	if err != nil {
		return StoredAttachment{}, notStoredNetwork, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return StoredAttachment{}, notStoredNetwork, fmt.Errorf("discord: attachment download returned HTTP %d", response.StatusCode)
	}
	// Read at most one byte past the per-file limit so the store's own check
	// rejects an oversized body instead of the transport truncating it silently.
	// The stream is copied by the store in chunks, never buffered whole.
	body := io.LimitReader(response.Body, limits.MaxFileBytes+1)
	// An empty or generic content type asks the store to detect the type from the
	// leading bytes, so a mislabelled file is classified by its magic numbers.
	declared := strings.TrimSpace(attachment.ContentType)
	if declared == "" || declared == "application/octet-stream" {
		declared = ""
	}
	ref, err := a.Store.PutWithContentType(downloadCtx, sessionID, displayName(attachment.Name), declared, body)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return StoredAttachment{}, notStoredCancel, err
		}
		if errors.Is(err, filestore.ErrFileTooLarge) || errors.Is(err, filestore.ErrSessionTooLarge) {
			return StoredAttachment{}, notStoredSize, err
		}
		return StoredAttachment{}, notStoredRejected, err
	}
	return StoredAttachment{
		RefID:       ref.ID,
		SourceID:    attachment.ID,
		Name:        ref.Name,
		ContentType: ref.ContentType,
		Size:        ref.Size,
		Path:        ref.Path,
	}, "", nil
}

// AttachmentNotes renders the reference text appended to an inbound message.
// Stored files come first, then problems, so a partially successful message
// still reads in delivery order.
func AttachmentNotes(stored []StoredAttachment, problems []AttachmentProblem) string {
	var lines []string
	for _, file := range stored {
		size := ""
		if file.Size > 0 {
			size = ", " + formatBytes(file.Size)
		}
		lines = append(lines, fmt.Sprintf("[attached: %s (%s%s)]",
			referenceToken(file.Name, "attachment"), referenceToken(file.ContentType, "file"), size))
	}
	for _, problem := range problems {
		size := ""
		if problem.Size > 0 {
			size = ", " + formatBytes(problem.Size)
		}
		lines = append(lines, fmt.Sprintf("[attachment unavailable: %s%s; %s]",
			referenceToken(problem.Name, "file"), size, referenceToken(problem.Reason, "not stored")))
	}
	return strings.Join(lines, "\n")
}

// referenceToken keeps one token of a reference line short and single-line,
// because it becomes part of the model-visible turn text and may be quoted back
// into a channel.
func referenceToken(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	return truncateOneLine(value, maxReferenceRunes)
}

// displayName keeps a user-supplied filename out of the text path as anything
// but a single bounded token. The store sanitises the record it keeps as well,
// and no path is ever built from this value.
func displayName(name string) string { return referenceToken(name, "attachment") }

// formatBytes renders a size the way a reference line promises: 12.3 KiB.
func formatBytes(size int64) string {
	if size < 0 {
		size = 0
	}
	if size < 1024 {
		return strconv.FormatInt(size, 10) + " B"
	}
	value := float64(size)
	for _, unit := range []string{"KiB", "MiB", "GiB", "TiB"} {
		value /= 1024
		if value < 1024 || unit == "TiB" {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return strconv.FormatInt(size, 10) + " B"
}

// parseSize reads back one inbound size field defensively; a hand-built
// InputMessage with a garbage size must not poison the reference text.
func parseSize(value string) int64 {
	size, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || size < 0 {
		return 0
	}
	return size
}

// StoredFromMetadata rebuilds the stored references of an input the gateway
// produced. It is exported for callers that render intake status, and it is the
// exact inverse of the ToInput schema: only attachment_<i>_* keys are read, and
// the count governs the loop so a stray key cannot invent a file.
func StoredFromMetadata(metadata map[string]string) []StoredAttachment {
	count := parseSize(metadata[MetaAttachmentCount])
	if count <= 0 {
		return nil
	}
	if count > 1000 {
		count = 1000
	}
	out := make([]StoredAttachment, 0, count)
	for index := 0; int64(index) < count; index++ {
		ref := metadata[AttachmentKey(index, MetaAttachmentID)]
		if strings.TrimSpace(ref) == "" {
			continue
		}
		out = append(out, StoredAttachment{
			RefID:       ref,
			Name:        metadata[AttachmentKey(index, MetaAttachmentName)],
			ContentType: metadata[AttachmentKey(index, MetaAttachmentContentType)],
			Size:        parseSize(metadata[AttachmentKey(index, MetaAttachmentSize)]),
			Path:        metadata[AttachmentKey(index, MetaAttachmentPath)],
		})
	}
	return out
}

// outboundRequest is the transport's reading of an output's file references.
type outboundRequest struct {
	ids      []string
	manifest bool
	latest   bool
	raw      string
}

// outboundRequested reports what this output asks the transport to upload.
//
// The canonical path only carries text, so the only honest way for a worker or a
// tool to name a file is a reference the store already handed out. "manifest"
// and "latest" exist because a design file a worker produced lands in its own
// session's store before any core plumbing can name its opaque ID.
//
// A raw value — inline bytes, a data URI, a base64 blob — is never honoured, and
// the refusal is reported as a safe failure indicator instead of silently
// uploading nothing (REQ-022, REQ-026).
func outboundRequested(metadata map[string]string) (outboundRequest, bool) {
	if metadata == nil {
		return outboundRequest{}, false
	}
	request := outboundRequest{ids: splitList(metadata[MetaOutAttachmentIDs])}
	switch strings.ToLower(strings.TrimSpace(metadata[MetaOutAttachments])) {
	case "":
	case OutboundModeManifest:
		request.manifest = true
	case OutboundModeLatest:
		request.latest = true
	default:
		// An unrecognised mode is a caller mistake worth reporting; it is kept as
		// raw so the upload is refused rather than guessed at.
		request.raw = metadata[MetaOutAttachments]
	}
	if raw := strings.TrimSpace(metadata[MetaOutAttachmentRaw]); raw != "" && len(request.ids) == 0 && !request.manifest && !request.latest {
		request.raw = raw
	}
	return request, len(request.ids) > 0 || request.manifest || request.latest || request.raw != ""
}

// splitList reads a comma-separated metadata list, dropping blanks so an empty
// or trailing-comma value asks for nothing rather than for an empty ID.
func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// outboundAttachment is one resolved file plus the handle that must be closed
// after the upload finishes.
type outboundAttachment struct {
	file   *discordgo.File
	closer io.Closer
}

func (o outboundAttachment) close() {
	if o.closer != nil {
		_ = o.closer.Close()
	}
}

// fileSender is the transport-side capability needed to upload. The plain Sender
// interface only carries text, so a Gateway (or a test fake) advertises this
// separately and text-only senders keep working unchanged.
type fileSender interface {
	SendFiles(ctx context.Context, channelID, content string, files []*discordgo.File) error
}

// resolveOutbound maps an output's session to the files its metadata asks for.
// It returns the upload handles and the safe notes to surface next to them.
// Every failure path yields a readable indicator instead of a silent no-op, and
// none of them echoes a path, an ID, or another session's file (REQ-022).
func (a *Attachments) resolveOutbound(ctx context.Context, sessionID string, request outboundRequest) ([]outboundAttachment, []string) {
	if request.raw != "" {
		return nil, []string{outboundInlineNote}
	}
	if !a.enabled() {
		return nil, []string{outboundDisabledNote}
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, []string{outboundNoSessionNote}
	}
	refs, notes := a.outboundRefs(ctx, sessionID, request)
	files := make([]outboundAttachment, 0, len(refs))
	for _, meta := range refs {
		if meta.Size > a.sendFileBytes() {
			notes = append(notes, fmt.Sprintf("⚠️ %s was not sent; it is larger than the send size limit", referenceToken(meta.Name, meta.ID)))
			continue
		}
		reader, _, err := a.Store.Get(ctx, sessionID, meta.ID)
		if err != nil {
			notes = append(notes, fmt.Sprintf("⚠️ %s was not sent; it could not be read from the attachment store", referenceToken(meta.Name, meta.ID)))
			continue
		}
		name := referenceToken(meta.Name, "attachment")
		contentType := strings.TrimSpace(meta.ContentType)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		files = append(files, outboundAttachment{file: &discordgo.File{Name: name, ContentType: contentType, Reader: reader}, closer: reader})
	}
	return files, notes
}

// outboundRefs turns the requested references into store records. Explicit IDs
// always win, because they are the narrowest statement of intent; a mode only
// expands the set when no ID was named. An unknown, malformed, or other-session
// ID is reported as unreadable rather than echoed, so a reference cannot probe
// another session's files: every lookup goes through the store, which scopes
// itself to the caller's session key.
func (a *Attachments) outboundRefs(ctx context.Context, sessionID string, request outboundRequest) ([]filestore.Meta, []string) {
	var notes []string
	ids := request.ids
	switch {
	case len(ids) == 0 && request.latest:
		metas, err := a.Store.List(ctx, sessionID)
		if err != nil {
			return nil, []string{outboundListFailure}
		}
		if latest := newestRef(metas); latest != nil {
			return []filestore.Meta{*latest}, notes
		}
		return nil, notes
	case len(ids) == 0 && request.manifest:
		metas, err := a.Store.List(ctx, sessionID)
		if err != nil {
			return nil, []string{outboundListFailure}
		}
		refs := make([]filestore.Meta, 0, len(metas))
		for _, meta := range metas {
			if len(refs) >= a.sendFileCount() {
				notes = append(notes, outboundCountNote)
				break
			}
			refs = append(refs, meta)
		}
		return refs, notes
	}
	limit := a.sendFileCount()
	refs := make([]filestore.Meta, 0, len(ids))
	for _, id := range ids {
		if len(refs) >= limit {
			notes = append(notes, outboundCountNote)
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, append(notes, outboundCancelNote)
		}
		_, meta, err := a.Store.Get(ctx, sessionID, id)
		if err != nil {
			notes = append(notes, outboundMissingNote)
			continue
		}
		refs = append(refs, meta)
	}
	return refs, notes
}

// Safe outbound notes. None of them names a path, an ID, or another session.
const (
	outboundListFailure   = "⚠️ no file was sent; the attachment list could not be read"
	outboundCountNote     = "⚠️ some files were not sent; the per-message file limit was reached"
	outboundCancelNote    = "⚠️ the remaining files were not sent; the turn was cancelled"
	outboundMissingNote   = "⚠️ a requested file was not sent; it is not an attachment of this session"
	outboundDisabledNote  = "⚠️ no file was sent; attachment storage is disabled"
	outboundNoSessionNote = "⚠️ no file was sent; the output has no session"
	outboundInlineNote    = "⚠️ no file was sent; inline file content is not supported, pass an attachment reference instead"
)

// newestRef picks the newest attachment of a session manifest by recorded
// creation time, falling back to the last entry when the times are indistinct,
// so "latest" stays predictable for files stored in the same second.
func newestRef(metas []filestore.Meta) *filestore.Meta {
	if len(metas) == 0 {
		return nil
	}
	newest := metas[0]
	for _, meta := range metas[1:] {
		if !meta.CreatedAt.Before(newest.CreatedAt) {
			newest = meta
		}
	}
	return &newest
}

// markSent records that this turn already delivered a set of references, so a
// terminal trace event and a final output for the same turn cannot duplicate an
// upload. The set is cleared when a new turn starts on the channel.
func (a *Attachments) markSent(channelID, key string) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sent == nil {
		a.sent = make(map[string]map[string]bool)
	}
	seen := a.sent[channelID]
	if seen == nil {
		seen = make(map[string]bool)
		a.sent[channelID] = seen
	}
	if seen[key] {
		return false
	}
	seen[key] = true
	return true
}

// sentKey identifies one outbound file request inside one turn.
func sentKey(request outboundRequest) string {
	if request.manifest {
		return "manifest:" + strings.Join(request.ids, ",")
	}
	if request.latest {
		return "latest"
	}
	if request.raw != "" {
		return "raw"
	}
	return strings.Join(request.ids, ",")
}

// forgetSent clears the per-turn record when a new turn starts on a channel.
func (a *Attachments) forgetSent(channelID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sent, channelID)
}
