package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Tulipskun/ai/runtime/filestore"
	"github.com/Tulipskun/ai/sdk"
)

// Attachment read bounds. Both are tool-side caps: the store may legitimately
// hold a much larger file, so one tool call never pulls an unbounded payload
// into a tool result. The byte cap matches the web_fetch response budget, and
// as in web_fetch a byte limit and a text-character limit both set the
// truncated flag. The character caps stay an order of magnitude tighter than
// web_fetch because an attachment result is persisted in the worker session
// database and summarised onward (CON-003), and because raw bytes and base64
// must never travel through the canonical SDK text path (REQ-016, REQ-019,
// REQ-026).
const (
	maxAttachmentReadBytes = int64(2 << 20)
	maxAttachmentReadChars = 20000
	maxAttachmentTextChars = 4000
	attachmentSniffBytes   = 512
)

// errNoAttachmentStore is returned by every attachment tool when no store was
// injected, so the older constructors keep working without panicking.
var errNoAttachmentStore = errors.New("attachment store is not configured")

// AttachmentStore is the read-only view of the attachment file store these
// tools need. The method set matches the real *filestore.Store signatures so
// cmd/ai injects the concrete store without an adapter. Only Get returns file
// content, and only for the session key the caller supplies; traversal,
// withinRoot, and symlink discipline stay inside the store, which uses the same
// safePath/withinRoot style this module uses for the workspace (REQ-026).
// filestore is a standard-library-only leaf package with no transport or
// provider knowledge, so naming its types here adds no import cycle and keeps
// CON-004 intact.
type AttachmentStore interface {
	List(ctx context.Context, sessionKey string) ([]filestore.Meta, error)
	Get(ctx context.Context, sessionKey, refID string) (io.ReadCloser, filestore.Meta, error)
	Manifest(ctx context.Context, sessionKey string) (filestore.Manifest, error)
}

// attachmentFile is the only file shape an attachment tool puts in front of the
// model: an opaque reference carrying name, content type, size, and the relative
// path inside the store (REQ-016).
type attachmentFile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Path        string `json:"path"`
	CreatedAt   string `json:"created_at,omitempty"`
}

func newAttachmentFile(meta filestore.Meta) attachmentFile {
	file := attachmentFile{ID: meta.ID, Name: meta.Name, ContentType: meta.ContentType, Size: meta.Size, Path: meta.Path}
	if !meta.CreatedAt.IsZero() {
		file.CreatedAt = meta.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return file
}

// SetAttachmentStore injects the store used by list_attachments,
// read_attachment, and describe_attachment. It is a setter because cmd/ai opens
// the store from runtime configuration after the registry is built; passing nil
// disables the tools again with a clear error.
func (r *Registry) SetAttachmentStore(store AttachmentStore) {
	r.attachmentMu.Lock()
	defer r.attachmentMu.Unlock()
	r.attachments = store
}

// attachmentStore returns the injected store or an explicit error. A registry
// built by NewRegistry/NewRegistryWithBrowser has no store, and a runtime with
// attachments disabled has none either; both must fail loudly, not panic.
func (r *Registry) attachmentStore() (AttachmentStore, error) {
	r.attachmentMu.RLock()
	defer r.attachmentMu.RUnlock()
	if r.attachments == nil {
		return nil, errNoAttachmentStore
	}
	return r.attachments, nil
}

// attachmentSession binds every attachment lookup to the session identity the
// SDK put on the tool context. The session key is never a tool argument, so the
// model cannot name another session and read its files.
func attachmentSession(ctx context.Context) (string, error) {
	sessionID := sdk.SessionIDFromContext(ctx)
	if strings.TrimSpace(sessionID) == "" {
		return "", errors.New("session_id is required for attachment tools")
	}
	return sessionID, nil
}

type listAttachmentsResult struct {
	SessionScoped bool             `json:"session_scoped"`
	Count         int              `json:"count"`
	TotalBytes    int64            `json:"total_bytes"`
	Attachments   []attachmentFile `json:"attachments"`
}

// listAttachmentsHandler returns references only; it never opens a file.
func (r *Registry) listAttachmentsHandler() handler {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		store, err := r.attachmentStore()
		if err != nil {
			return "", err
		}
		if err := json.Unmarshal(raw, &struct{}{}); err != nil {
			return "", err
		}
		sessionID, err := attachmentSession(ctx)
		if err != nil {
			return "", err
		}
		metas, err := store.List(ctx, sessionID)
		if err != nil {
			return "", err
		}
		result := listAttachmentsResult{SessionScoped: true, Attachments: make([]attachmentFile, 0, len(metas))}
		for _, meta := range metas {
			result.Attachments = append(result.Attachments, newAttachmentFile(meta))
			if meta.Size > 0 {
				result.TotalBytes += meta.Size
			}
		}
		result.Count = len(result.Attachments)
		return encodeAttachmentResult(result)
	}
}

type readAttachmentArgs struct {
	RefID    string `json:"ref_id"`
	MaxBytes int64  `json:"max_bytes"`
}

type readAttachmentResult struct {
	File      attachmentFile `json:"file"`
	Kind      string         `json:"kind"`
	Charset   string         `json:"charset"`
	Text      string         `json:"text"`
	Chars     int            `json:"chars"`
	Lines     int            `json:"lines"`
	BytesRead int64          `json:"bytes_read"`
	Truncated bool           `json:"truncated"`
}

// readAttachmentHandler returns the text of one attachment owned by the current
// session. Binary, image, and PDF payloads are refused with their content type
// and size plus a pointer to describe_attachment, because putting bytes in a
// tool result would violate the text-path rules.
func (r *Registry) readAttachmentHandler() handler {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args readAttachmentArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", err
		}
		stored, err := r.openAttachment(ctx, args.RefID, args.MaxBytes)
		if err != nil {
			return "", err
		}
		if stored.kind != attachmentText {
			return "", fmt.Errorf("attachment %q is %s (%d bytes) and is not readable text; use describe_attachment for its metadata",
				stored.file.Name, stored.contentTypeLabel(), stored.file.Size)
		}
		text := strings.TrimRight(string(stored.data), "\x00")
		if stored.truncated {
			// The byte cap can cut a multi-byte rune in half; that partial tail
			// is not invalid content, so it is dropped before the check instead
			// of turning a successful read into an error.
			text = trimIncompleteUTF8(text)
		}
		if !utf8.ValidString(text) {
			return "", fmt.Errorf("attachment %q declares %s but its bytes are not valid UTF-8; use describe_attachment",
				stored.file.Name, stored.contentTypeLabel())
		}
		result := readAttachmentResult{File: stored.file, Kind: stored.kind.String(), Charset: "utf-8", BytesRead: stored.bytesRead, Truncated: stored.truncated}
		result.Text, result.Truncated = clampRunes(text, maxAttachmentReadChars, result.Truncated)
		result.Chars = len([]rune(result.Text))
		result.Lines = countLines(result.Text)
		return encodeAttachmentResult(result)
	}
}

type describeAttachmentResult struct {
	File             attachmentFile `json:"file"`
	Kind             string         `json:"kind"`
	Format           string         `json:"format,omitempty"`
	Width            int            `json:"width,omitempty"`
	Height           int            `json:"height,omitempty"`
	PageCount        int            `json:"page_count,omitempty"`
	PagesDiscovered  bool           `json:"pages_discovered,omitempty"`
	TextOperators    int            `json:"text_operators,omitempty"`
	Images           int            `json:"embedded_images,omitempty"`
	ExtractedFromPDF bool           `json:"pdf_text_extracted,omitempty"`
	Charset          string         `json:"charset,omitempty"`
	Text             string         `json:"text,omitempty"`
	Chars            int            `json:"chars,omitempty"`
	Lines            int            `json:"lines,omitempty"`
	BytesProcessed   int64          `json:"bytes_processed"`
	Truncated        bool           `json:"truncated"`
	Notes            []string       `json:"notes,omitempty"`
}

// describeAttachmentHandler reports the standard-library-only analysis of one
// attachment: size and content type from the manifest, PNG/JPEG/GIF dimensions
// from image.DecodeConfig, and for PDFs a page count plus best-effort text from
// the content streams. It never returns the file's bytes.
func (r *Registry) describeAttachmentHandler() handler {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args readAttachmentArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", err
		}
		stored, err := r.openAttachment(ctx, args.RefID, args.MaxBytes)
		if err != nil {
			return "", err
		}
		result := describeAttachmentResult{
			File:           stored.file,
			Kind:           stored.kind.String(),
			BytesProcessed: stored.bytesRead,
			Truncated:      stored.truncated,
		}
		switch stored.kind {
		case attachmentImage:
			width, height, format, err := decodeImageDimensions(stored.data)
			if err != nil {
				result.Format = format
				result.Notes = append(result.Notes, fmt.Sprintf("dimensions are not readable with the standard library image decoders (%s): %v", stored.contentTypeLabel(), err))
				break
			}
			result.Format = format
			result.Width = width
			result.Height = height
		case attachmentPDF:
			summary := summarizePDF(stored.data)
			result.Format = "pdf"
			result.PageCount = summary.pages
			result.PagesDiscovered = summary.pagesDiscovered
			result.Images = summary.images
			result.TextOperators = summary.textOperators
			text, truncated := clampRunes(summary.text, maxAttachmentTextChars, stored.truncated)
			result.Text = text
			result.Chars = len([]rune(result.Text))
			result.Lines = countLines(result.Text)
			result.ExtractedFromPDF = result.Chars > 0
			result.Truncated = truncated
			result.Notes = append(result.Notes, summary.notes...)
			if stored.truncated {
				result.Notes = append(result.Notes, fmt.Sprintf("only the first %d bytes were read, so later streams are not analysed", stored.bytesRead))
			}
		case attachmentText:
			result.Format = "text"
			result.Charset = "utf-8"
			text := strings.TrimRight(string(stored.data), "\x00")
			if stored.truncated {
				text = trimIncompleteUTF8(text)
			}
			if !utf8.ValidString(text) {
				result.Kind = attachmentBinary.String()
				result.Format = sniffFormat(stored.snippet)
				result.Charset = ""
				result.Notes = append(result.Notes, "the recorded content type says text but the bytes are not valid UTF-8")
				break
			}
			clamped, truncated := clampRunes(text, maxAttachmentTextChars, stored.truncated)
			result.Text = clamped
			result.Chars = len([]rune(clamped))
			result.Lines = countLines(clamped)
			result.Truncated = truncated
		case attachmentAudio, attachmentVideo, attachmentArchive, attachmentBinary:
			result.Format = sniffFormat(stored.snippet)
			result.Notes = append(result.Notes, "attachment tools read metadata only for this kind; no standard-library decoder summarises it")
		}
		return encodeAttachmentResult(result)
	}
}

// storedAttachment is one attachment already fetched for the current session.
type storedAttachment struct {
	file      attachmentFile
	kind      attachmentKind
	snippet   []byte
	data      []byte
	bytesRead int64
	truncated bool
}

func (s *storedAttachment) contentTypeLabel() string {
	if strings.TrimSpace(s.file.ContentType) == "" {
		return "unknown content type"
	}
	return s.file.ContentType
}

// openAttachment resolves a reference inside the current session and reads a
// bounded prefix of it. An unknown, malformed, or other-session reference fails
// here with the store's own error, which never discloses another session's
// files.
func (r *Registry) openAttachment(ctx context.Context, refID string, maxBytes int64) (*storedAttachment, error) {
	store, err := r.attachmentStore()
	if err != nil {
		return nil, err
	}
	sessionID, err := attachmentSession(ctx)
	if err != nil {
		return nil, err
	}
	refID = strings.TrimSpace(refID)
	if refID == "" {
		return nil, errors.New("ref_id is required")
	}
	if !attachmentRefIDValid(refID) {
		return nil, fmt.Errorf("invalid attachment reference %q", refID)
	}
	limit := attachmentReadLimit(maxBytes)
	reader, meta, err := store.Get(ctx, sessionID, refID)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read attachment %q: %w", meta.ID, err)
	}
	truncated := int64(len(data)) > limit
	if truncated {
		data = data[:limit]
	}
	snippet := data
	if len(snippet) > attachmentSniffBytes {
		snippet = snippet[:attachmentSniffBytes]
	}
	return &storedAttachment{
		file:      newAttachmentFile(meta),
		kind:      resolveAttachmentKind(meta.ContentType, snippet),
		snippet:   snippet,
		data:      data,
		bytesRead: int64(len(data)),
		truncated: truncated,
	}, nil
}

// attachmentReadLimit clamps a caller-supplied byte budget to the tool cap.
func attachmentReadLimit(maxBytes int64) int64 {
	if maxBytes <= 0 || maxBytes > maxAttachmentReadBytes {
		return maxAttachmentReadBytes
	}
	return maxBytes
}

// attachmentRefIDValid mirrors the store's opaque identifier shape so a
// traversal or otherwise malformed reference is refused before any path
// arithmetic. The store enforces the same rule again, plus withinRoot and
// symlink checks; this is defence in depth only.
func attachmentRefIDValid(id string) bool {
	if len(id) != 22 {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil || len(raw) != 16 {
		return false
	}
	return base64.RawURLEncoding.EncodeToString(raw) == id
}

func encodeAttachmentResult(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// clampRunes truncates on rune boundaries and reports whether it shortened the
// text, so a truncation flag raised earlier is preserved.
func clampRunes(text string, limit int, truncated bool) (string, bool) {
	runes := []rune(text)
	if len(runes) <= limit {
		return text, truncated
	}
	return string(runes[:limit]), true
}

func countLines(text string) int {
	if text == "" {
		return 0
	}
	return 1 + strings.Count(strings.TrimRight(text, "\n"), "\n")
}

// trimIncompleteUTF8 drops a trailing partial rune, which a byte-bounded read
// can produce even from valid UTF-8 text.
func trimIncompleteUTF8(text string) string {
	for len(text) > 0 {
		if utf8.ValidString(text) {
			return text
		}
		_, size := utf8.DecodeLastRuneInString(text)
		if size == 1 && text[len(text)-1] < utf8.RuneSelf {
			// A single invalid non-ASCII byte is not a truncation artifact.
			return text
		}
		text = text[:len(text)-1]
	}
	return text
}

// attachmentKind classifies an attachment so a tool can pick its summarizer.
type attachmentKind int

const (
	attachmentUnknown attachmentKind = iota
	attachmentText
	attachmentImage
	attachmentPDF
	attachmentArchive
	attachmentAudio
	attachmentVideo
	attachmentBinary
)

func (k attachmentKind) String() string {
	switch k {
	case attachmentText:
		return "text"
	case attachmentImage:
		return "image"
	case attachmentPDF:
		return "pdf"
	case attachmentArchive:
		return "archive"
	case attachmentAudio:
		return "audio"
	case attachmentVideo:
		return "video"
	case attachmentBinary:
		return "binary"
	default:
		return "binary"
	}
}

// resolveAttachmentKind prefers the content type recorded by the store and falls
// back to magic bytes when that type is missing or generic. Magic bytes win for
// a real binary payload even when the declared type claims text, because a
// mislabelled upload must never be dumped into the text path.
func resolveAttachmentKind(contentType string, snippet []byte) attachmentKind {
	if len(snippet) == 0 {
		return attachmentText
	}
	declared := kindFromMediaType(contentType)
	sniffed := kindFromMagic(snippet)
	switch {
	case declared == attachmentText:
		if sniffed == attachmentImage || sniffed == attachmentPDF || sniffed == attachmentArchive || sniffed == attachmentAudio || sniffed == attachmentVideo {
			return sniffed
		}
		return attachmentText
	case declared != attachmentUnknown:
		return declared
	case sniffed != attachmentUnknown:
		return sniffed
	default:
		return attachmentBinary
	}
}

func kindFromMediaType(contentType string) attachmentKind {
	media := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if media == "" {
		return attachmentUnknown
	}
	if strings.HasPrefix(media, "text/") {
		return attachmentText
	}
	switch media {
	case "application/json", "application/xml", "application/javascript", "application/typescript",
		"application/x-javascript", "application/sql", "application/x-sh", "application/toml",
		"application/x-yaml", "application/yaml", "application/x-httpd-php", "message/rfc822",
		"application/x-www-form-urlencoded", "application/graphql", "application/csv",
		"application/vnd.apple.mpegurl", "application/rss+xml", "application/atom+xml", "application/xhtml+xml":
		return attachmentText
	case "image/svg+xml":
		return attachmentText
	case "application/pdf", "application/x-pdf":
		return attachmentPDF
	case "image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp", "image/bmp",
		"image/x-ms-bmp", "image/tiff", "image/avif", "image/heic", "image/heif", "image/vnd.microsoft.icon":
		return attachmentImage
	case "audio/mpeg", "audio/ogg", "audio/wav", "audio/x-wav", "audio/webm", "audio/mp4",
		"audio/aac", "audio/flac", "audio/x-m4a", "audio/basic", "audio/3gpp":
		return attachmentAudio
	case "video/mp4", "video/webm", "video/quicktime", "video/x-msvideo", "video/mpeg",
		"video/x-matroska", "video/3gpp", "video/3gpp2", "video/x-flv":
		return attachmentVideo
	case "application/zip", "application/x-zip-compressed", "application/gzip", "application/x-gzip",
		"application/x-tar", "application/x-7z-compressed", "application/x-bzip2", "application/x-bzip",
		"application/x-rar-compressed", "application/vnd.rar", "application/x-xz", "application/x-compress",
		"application/zstd", "application/java-archive", "application/x-cpio":
		return attachmentArchive
	case "application/octet-stream":
		// Generic: let the magic bytes decide.
		return attachmentUnknown
	default:
		return attachmentUnknown
	}
}

// kindFromMagic classifies from a byte prefix only.
func kindFromMagic(snippet []byte) attachmentKind {
	if len(snippet) == 0 {
		return attachmentUnknown
	}
	switch sniffFormat(snippet) {
	case "pdf":
		return attachmentPDF
	case "zip", "gzip", "tar", "7z", "bz2", "xz", "zstd":
		return attachmentArchive
	case "png", "jpeg", "gif", "webp", "bmp":
		return attachmentImage
	case "wav", "mp3", "flac", "ogg":
		return attachmentAudio
	case "mp4", "matroska", "avi", "quicktime":
		return attachmentVideo
	}
	switch media := http.DetectContentType(snippet); {
	case strings.HasPrefix(media, "text/"):
		return attachmentText
	case strings.HasPrefix(media, "image/"):
		return attachmentImage
	case strings.HasPrefix(media, "audio/"):
		return attachmentAudio
	case strings.HasPrefix(media, "video/"):
		return attachmentVideo
	default:
		return attachmentUnknown
	}
}

// sniffFormat names a container or codec visible in the leading bytes. It only
// reads magic numbers, never a whole-file index, so it stays cheap.
func sniffFormat(snippet []byte) string {
	switch {
	case hasPrefixBytes(snippet, "%PDF"):
		return "pdf"
	case hasPrefixBytes(snippet, "\x89PNG\r\n\x1a\n"):
		return "png"
	case hasPrefixBytes(snippet, "\xff\xd8\xff"):
		return "jpeg"
	case hasPrefixBytes(snippet, "GIF87a"), hasPrefixBytes(snippet, "GIF89a"):
		return "gif"
	case hasPrefixBytes(snippet, "BM"):
		return "bmp"
	case hasPrefixBytes(snippet, "PK\x03\x04"), hasPrefixBytes(snippet, "PK\x05\x06"), hasPrefixBytes(snippet, "PK\x06\x06"):
		return "zip"
	case hasPrefixBytes(snippet, "\x1f\x8b"):
		return "gzip"
	case hasPrefixBytes(snippet, "7z\xbc\xaf\x27\x1c"):
		return "7z"
	case hasPrefixBytes(snippet, "BZh"):
		return "bz2"
	case hasPrefixBytes(snippet, "\xfd7zXZ\x00"):
		return "xz"
	case hasPrefixBytes(snippet, "\x28\xb5\x2f\xfd"):
		return "zstd"
	case len(snippet) >= 262 && string(snippet[257:262]) == "ustar":
		return "tar"
	case hasPrefixBytes(snippet, "RIFF") && len(snippet) >= 12 && string(snippet[8:12]) == "WEBP":
		return "webp"
	case hasPrefixBytes(snippet, "RIFF") && len(snippet) >= 12 && string(snippet[8:12]) == "WAVE":
		return "wav"
	case hasPrefixBytes(snippet, "\x1a\x45\xdf\xa3"):
		return "matroska"
	case hasPrefixBytes(snippet, "OggS"):
		return "ogg"
	case hasPrefixBytes(snippet, "fLaC"):
		return "flac"
	case hasPrefixBytes(snippet, "ID3"), len(snippet) >= 2 && snippet[0] == 0xFF && snippet[1]&0xE0 == 0xE0:
		return "mp3"
	case len(snippet) >= 12 && string(snippet[4:8]) == "ftyp":
		if brand := string(snippet[8:12]); strings.HasPrefix(brand, "qt") {
			return "quicktime"
		}
		return "mp4"
	case hasPrefixBytes(snippet, "\x1a\x00\x00\x00"), hasPrefixBytes(snippet, "AVI "):
		return "avi"
	default:
		return ""
	}
}

func hasPrefixBytes(data []byte, prefix string) bool {
	return len(data) >= len(prefix) && string(data[:len(prefix)]) == prefix
}

// decodeImageDimensions returns width and height for the image formats the
// standard library registers by default (PNG, JPEG, GIF). Anything else is an
// error the caller reports as a note; no third-party decoder is added.
func decodeImageDimensions(data []byte) (int, int, string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, sniffFormat(truncateSnippet(data)), err
	}
	return config.Width, config.Height, format, nil
}

func truncateSnippet(data []byte) []byte {
	if len(data) > attachmentSniffBytes {
		return data[:attachmentSniffBytes]
	}
	return data
}
