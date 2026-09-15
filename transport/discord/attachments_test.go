package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tulipskun/ai/runtime/filestore"
	"github.com/Tulipskun/ai/sdk"

	"github.com/bwmarrin/discordgo"
)

// Every test in this file runs offline: downloads go through an injected
// *http.Client (a stub RoundTripper or a loopback httptest server), the store is
// a real filestore under t.TempDir(), and uploads are captured by a recording
// sender or by a RoundTripper in place of Discord's REST endpoint. No live
// Discord traffic, token, or credential is involved (REQ-025, REQ-026).

const (
	testSession  = "discord:channel:1234567890"
	testChannel  = "1234567890"
	testMessage  = "900000000000000001"
	testAuthor   = "500000000000000000"
	testAuthorNm = "Yuuta"
)

var pngBytes = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x87, 0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
	0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05,
	0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00,
	0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60,
	0x82,
}

// textAttachment is a small plain-text body, so magic-byte detection can be
// checked against a declared type that disagrees with it.
var textAttachment = []byte("hello from an attached file\nsecond line\n")

// newTestStore opens a real attachment store under a temp state root with tight
// limits, so size behaviour is exercised against production code rather than a
// fake (CON-011).
func newTestStore(t *testing.T, maxFile, maxSession int64) *filestore.Store {
	t.Helper()
	store, err := filestore.OpenUnder(t.TempDir(), filestore.DefaultRoot, filestore.Limits{
		MaxFileBytes:    maxFile,
		MaxSessionBytes: maxSession,
		TTL:             time.Hour,
	})
	if err != nil {
		t.Fatalf("open attachment store: %v", err)
	}
	return store
}

// stubRoundTripper answers every request with a fixed status and body, and
// records the URLs it was asked for. It is the injectable-client hook REQ-025
// asks for: the transport must never reach a real CDN in a test.
type stubRoundTripper struct {
	mu      sync.Mutex
	status  int
	body    []byte
	err     error
	headers http.Header
	urls    []string
	// byPath lets one stub serve several bodies, keyed by URL path.
	byPath map[string][]byte
}

func (s *stubRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.urls = append(s.urls, req.URL.String())
	s.headers = req.Header.Clone()
	if s.err != nil {
		return nil, s.err
	}
	// A real *http.Client refuses a location that is not absolute before any
	// transport runs, so the stub does too; otherwise a test could "download" a
	// relative URL and mask a bad download location.
	if !req.URL.IsAbs() {
		return nil, fmt.Errorf("Get %q: unsupported protocol scheme", req.URL)
	}
	body := s.body
	status := s.status
	if s.byPath != nil {
		named, ok := s.byPath[req.URL.Path]
		if !ok {
			// A path the stub was not told about is a missing object on a real
			// CDN: a 404 whose body must never reach the model or a channel.
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("<html>secret internal detail</html>")),
				Request:    req,
			}, nil
		}
		body, status = named, http.StatusOK
	}
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    req,
	}, nil
}

func (s *stubRoundTripper) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.urls...)
}

func stubClient(rt http.RoundTripper) *http.Client {
	return &http.Client{Transport: rt, Timeout: 5 * time.Second}
}

func attachmentMessage(content string, files ...InputAttachment) InputMessage {
	return InputMessage{
		SessionID: testSession, ChannelID: testChannel, MessageID: testMessage,
		AuthorID: testAuthor, AuthorName: testAuthorNm, Content: content, Attachments: files,
	}
}

// ---------------------------------------------------------------------------
// intake: normalisation and conversion
// ---------------------------------------------------------------------------

func TestNormalizeMessageConvertsAttachmentsWithoutDownloading(t *testing.T) {
	wire := []*discordgo.MessageAttachment{
		{ID: "att-1", Filename: "design.png", ContentType: "image/png", Size: 67, URL: "https://cdn.example/design.png", ProxyURL: "https://proxy.example/design.png"},
		nil,
		{ID: "att-2", Filename: "notes.txt", Size: 36},
	}
	got, ok := normalizeMessage(discordMessage{
		ID: testMessage, ChannelID: testChannel, AuthorID: testAuthor, AuthorName: testAuthorNm,
		Content: "look at this", AuthorIsBot: false, Attachments: inputAttachments(wire),
	})
	if !ok {
		t.Fatal("message was rejected")
	}
	if len(got.Attachments) != 2 {
		t.Fatalf("attachments = %+v, want the two non-nil entries", got.Attachments)
	}
	first := got.Attachments[0]
	if first.ID != "att-1" || first.Name != "design.png" || first.ContentType != "image/png" || first.Size != 67 {
		t.Fatalf("first attachment = %+v", first)
	}
	if first.URL == "" || first.ProxyURL == "" {
		t.Fatalf("both download locations must survive conversion: %+v", first)
	}
	if got.Stored != nil || got.Problems != nil {
		t.Fatalf("normalisation must not resolve files: stored=%+v problems=%+v", got.Stored, got.Problems)
	}
	if got.Content != "look at this" {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestInputAttachmentsAndConvertAttachmentsAreNilSafe(t *testing.T) {
	if got := inputAttachments(nil); got != nil {
		t.Fatalf("inputAttachments(nil) = %+v", got)
	}
	if got := convertAttachments(nil); got != nil {
		t.Fatalf("convertAttachments(nil) = %+v", got)
	}
	source := []InputAttachment{{ID: "1", Name: "a.txt"}}
	clone := convertAttachments(source)
	clone[0].Name = "mutated"
	if source[0].Name != "a.txt" {
		t.Fatal("convertAttachments must copy the list, not alias it")
	}
}

func TestGatewayIntentsDoNotRequireGuildsForAttachments(t *testing.T) {
	intents := gatewayIntents()
	if intents&discordgo.IntentsGuilds != 0 {
		t.Fatal("attachment intake must not widen the intent surface with IntentsGuilds")
	}
	if intents&discordgo.IntentsGuildMessages == 0 || intents&discordgo.IntentsDirectMessages == 0 {
		t.Fatal("message intents are still required")
	}
}

// ---------------------------------------------------------------------------
// intake: download through the injected client into the store
// ---------------------------------------------------------------------------

func TestHydrateStoresAttachmentAndRecordsReference(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	stub := &stubRoundTripper{body: textAttachment}
	attachments := &Attachments{Store: store, Client: stubClient(stub)}
	message := attachmentMessage("read this", InputAttachment{ID: "att-1", Name: "notes.txt", ContentType: "text/plain", Size: int64(len(textAttachment)), URL: "https://cdn.example/notes.txt", ProxyURL: "https://proxy.example/notes.txt"})

	hydrated := attachments.Hydrate(context.Background(), message)

	if len(hydrated.Stored) != 1 {
		t.Fatalf("stored = %+v, want one reference", hydrated.Stored)
	}
	if len(hydrated.Problems) != 0 {
		t.Fatalf("problems = %+v", hydrated.Problems)
	}
	stored := hydrated.Stored[0]
	if stored.SourceID != "att-1" {
		t.Fatalf("SourceID = %q, want the Discord attachment ID", stored.SourceID)
	}
	if stored.Name != "notes.txt" || stored.Size != int64(len(textAttachment)) {
		t.Fatalf("stored = %+v", stored)
	}
	// A declared type is recorded verbatim by the store; only an absent or
	// generic one is detected from magic bytes (see the next test).
	if stored.ContentType != "text/plain" {
		t.Fatalf("content type = %q", stored.ContentType)
	}
	if !strings.HasPrefix(stored.Path, filepath.Base(testSession)) && !strings.Contains(stored.Path, "files/") {
		t.Fatalf("path = %q, want the store-relative path", stored.Path)
	}
	// The proxy location is the channel-scoped one and must win; the raw CDN URL
	// is only a fallback.
	urls := stub.seen()
	if len(urls) != 1 || urls[0] != "https://proxy.example/notes.txt" {
		t.Fatalf("downloaded %v, want only the proxy URL", urls)
	}
	// The bytes really landed in the store, addressed by the session the message
	// routes to.
	reader, meta, err := store.Get(context.Background(), testSession, stored.RefID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, textAttachment) {
		t.Fatalf("stored body = %q", body)
	}
	if meta.Name != "notes.txt" {
		t.Fatalf("manifest name = %q", meta.Name)
	}
}

func TestHydratePrefersDeclaredTypeAndDetectsFromMagicBytes(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	stub := &stubRoundTripper{byPath: map[string][]byte{"/png-magic": pngBytes, "/png-mislabelled": pngBytes, "/empty-type": textAttachment}}
	attachments := &Attachments{Store: store, Client: stubClient(stub)}

	// A declared type is recorded as-is (Discord already labelled the file).
	png := attachmentMessage("", InputAttachment{ID: "p", Name: "shot.png", URL: "https://cdn.invalid/png-magic", ContentType: "image/png", Size: int64(len(pngBytes))})
	hydrated := attachments.Hydrate(context.Background(), png)
	if len(hydrated.Stored) != 1 {
		t.Fatalf("stored = %+v", hydrated.Stored)
	}
	if got := hydrated.Stored[0].ContentType; got != "image/png" {
		t.Fatalf("declared type = %q, want image/png", got)
	}

	// An empty or generic type asks the store to classify by magic bytes, so a
	// mislabelled file is still identified as an image.
	for name, declared := range map[string]string{"empty": "", "generic": "application/octet-stream"} {
		message := attachmentMessage("", InputAttachment{ID: "m", Name: "mystery.png", URL: "https://cdn.invalid/png-mislabelled", ContentType: declared, Size: int64(len(pngBytes))})
		hydrated := attachments.Hydrate(context.Background(), message)
		if len(hydrated.Stored) != 1 {
			t.Fatalf("%s: stored = %+v", name, hydrated.Stored)
		}
		if got := hydrated.Stored[0].ContentType; !strings.HasPrefix(got, "image/png") && got != "image/png" {
			t.Fatalf("%s: detected type = %q, want image/png from magic bytes", name, got)
		}
	}

	// Detection also covers a file whose name lies about being text.
	message := attachmentMessage("", InputAttachment{ID: "e", Name: "not-text.txt", URL: "https://cdn.invalid/empty-type", Size: int64(len(textAttachment))})
	hydrated = attachments.Hydrate(context.Background(), message)
	if len(hydrated.Stored) != 1 {
		t.Fatalf("stored = %+v", hydrated.Stored)
	}
	if !strings.HasPrefix(hydrated.Stored[0].ContentType, "text/plain") {
		t.Fatalf("detected type = %q", hydrated.Stored[0].ContentType)
	}
}

func TestHydrateFallsBackToRawURLWhenProxyMissing(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	stub := &stubRoundTripper{body: textAttachment}
	attachments := &Attachments{Store: store, Client: stubClient(stub)}
	message := attachmentMessage("", InputAttachment{ID: "att", Name: "a.txt", URL: "https://cdn.example/a.txt", Size: int64(len(textAttachment))})
	if got := attachments.Hydrate(context.Background(), message); len(got.Stored) != 1 {
		t.Fatalf("stored = %+v", got.Stored)
	}
	urls := stub.seen()
	if len(urls) != 1 || urls[0] != "https://cdn.example/a.txt" {
		t.Fatalf("downloaded %v, want the CDN URL fallback", urls)
	}
}

func TestHydrateUsesHTTPTestServerLoopback(t *testing.T) {
	// A real HTTP server on loopback proves the injected client is a real
	// net/http path, not only a stubbed transport. Still no external network.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/good" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write(textAttachment)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	store := newTestStore(t, 1<<20, 4<<20)
	attachments := &Attachments{Store: store, Client: server.Client()}

	message := attachmentMessage("loopback", InputAttachment{ID: "ok", Name: "good.txt", URL: server.URL + "/good", Size: int64(len(textAttachment))})
	hydrated := attachments.Hydrate(context.Background(), message)
	if len(hydrated.Stored) != 1 {
		t.Fatalf("stored = %+v problems=%+v", hydrated.Stored, hydrated.Problems)
	}

	blocked := attachmentMessage("loopback", InputAttachment{ID: "no", Name: "forbidden.txt", URL: server.URL + "/forbidden"})
	hydrated = attachments.Hydrate(context.Background(), blocked)
	if len(hydrated.Stored) != 0 || len(hydrated.Problems) != 1 {
		t.Fatalf("stored=%+v problems=%+v", hydrated.Stored, hydrated.Problems)
	}
	if hydrated.Problems[0].Reason != notStoredNetwork {
		t.Fatalf("reason = %q", hydrated.Problems[0].Reason)
	}
}

func TestHydrateKeepsTurnWhenStoreDisabled(t *testing.T) {
	disabled := &Attachments{}
	message := attachmentMessage("", InputAttachment{ID: "att", Name: "a.txt", URL: "https://cdn.example/a.txt", Size: 10})
	hydrated := disabled.Hydrate(context.Background(), message)
	if len(hydrated.Stored) != 0 {
		t.Fatalf("stored = %+v, a disabled store must store nothing", hydrated.Stored)
	}
	if len(hydrated.Problems) != 1 || hydrated.Problems[0].Reason != notStoredDisabled {
		t.Fatalf("problems = %+v", hydrated.Problems)
	}
	// An attachment-only message must still carry text (REQ-025).
	if strings.TrimSpace(hydrated.Content) == "" {
		t.Fatal("attachment-only message became an empty turn")
	}
	if !strings.Contains(hydrated.Content, "a.txt") {
		t.Fatalf("content = %q", hydrated.Content)
	}
}

func TestHydrateIsIdempotentAcrossConsumers(t *testing.T) {
	// The gateway pump hydrates, then InputSource hydrates again. The second pass
	// must be a no-op: no second download, and no downgrade of real references
	// into "not stored" notes by a consumer that has no store.
	store := newTestStore(t, 1<<20, 4<<20)
	stub := &stubRoundTripper{body: textAttachment}
	attachments := &Attachments{Store: store, Client: stubClient(stub)}
	message := attachmentMessage("twice", InputAttachment{ID: "att", Name: "a.txt", URL: "https://cdn.example/a.txt", Size: int64(len(textAttachment))})

	first := attachments.Hydrate(context.Background(), message)
	if len(first.Stored) != 1 {
		t.Fatalf("first pass stored = %+v", first.Stored)
	}
	second := (&Attachments{}).Hydrate(context.Background(), first)
	if len(second.Stored) != 1 || len(second.Problems) != 0 {
		t.Fatalf("second pass lost the reference: stored=%+v problems=%+v", second.Stored, second.Problems)
	}
	if strings.Count(second.Content, "attachment unavailable") != 0 {
		t.Fatalf("second pass injected failure notes: %q", second.Content)
	}
	if strings.Count(second.Content, "attached:") != 1 {
		t.Fatalf("reference text duplicated: %q", second.Content)
	}
	if urls := stub.seen(); len(urls) != 1 {
		t.Fatalf("downloads = %v, want exactly one", urls)
	}
}

func TestHydrateWithoutAttachmentsLeavesMessageUntouched(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	attachments := &Attachments{Store: store, Client: stubClient(&stubRoundTripper{err: errors.New("must not be used")})}
	message := attachmentMessage("plain text")
	hydrated := attachments.Hydrate(context.Background(), message)
	if hydrated.Content != "plain text" || hydrated.Stored != nil || hydrated.Problems != nil {
		t.Fatalf("plain message changed: %+v", hydrated)
	}
}

func TestHydrateAppendsReferenceAfterExistingText(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	attachments := &Attachments{Store: store, Client: stubClient(&stubRoundTripper{body: textAttachment})}
	message := attachmentMessage("what is this?", InputAttachment{ID: "att", Name: "a.txt", URL: "https://cdn.example/a.txt", Size: int64(len(textAttachment))})
	hydrated := attachments.Hydrate(context.Background(), message)
	if !strings.HasPrefix(hydrated.Content, "what is this?") {
		t.Fatalf("user text must stay first: %q", hydrated.Content)
	}
	if !strings.Contains(hydrated.Content, "[attached: a.txt (text/plain") {
		t.Fatalf("reference line missing: %q", hydrated.Content)
	}
}

// ---------------------------------------------------------------------------
// intake: failures must never lose the turn
// ---------------------------------------------------------------------------

// budgetExhaustedStore opens a store whose whole session budget is already spent
// by one earlier attachment, so the next file must fail on the session limit
// rather than the per-file one. filestore requires the session budget to be at
// least the per-file limit, so the state is reached by filling the store instead
// of by configuring an impossible pair of numbers.
func budgetExhaustedStore(t *testing.T) *filestore.Store {
	t.Helper()
	store := newTestStore(t, 1024, 1024)
	if _, err := store.Put(context.Background(), testSession, "first.bin", bytes.NewReader(bytes.Repeat([]byte("y"), 1024))); err != nil {
		t.Fatalf("exhaust the session budget: %v", err)
	}
	return store
}

func TestHydrateFailureModesKeepTheTurn(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 4096)
	for name, test := range map[string]struct {
		store      *filestore.Store
		client     *http.Client
		attachment InputAttachment
		reason     string
	}{
		"transport error": {
			store:      newTestStore(t, 1<<20, 4<<20),
			client:     stubClient(&stubRoundTripper{err: errors.New("dial tcp: refused")}),
			attachment: InputAttachment{ID: "a", Name: "a.txt", URL: "https://cdn.example/a.txt", Size: int64(len(body))},
			reason:     notStoredNetwork,
		},
		"server error": {
			store:      newTestStore(t, 1<<20, 4<<20),
			client:     stubClient(&stubRoundTripper{status: http.StatusInternalServerError, body: []byte("<html>secret internal detail</html>")}),
			attachment: InputAttachment{ID: "b", Name: "b.txt", URL: "https://cdn.example/b.txt", Size: int64(len(body))},
			reason:     notStoredNetwork,
		},
		"declared size over limit": {
			store:      newTestStore(t, 1024, 4<<20),
			client:     stubClient(&stubRoundTripper{body: body}),
			attachment: InputAttachment{ID: "c", Name: "c.zip", URL: "https://cdn.example/c.zip", Size: 1 << 30},
			reason:     notStoredSize,
		},
		"body larger than limit": {
			store:      newTestStore(t, 1024, 4<<20),
			client:     stubClient(&stubRoundTripper{body: body}),
			attachment: InputAttachment{ID: "d", Name: "d.bin", URL: "https://cdn.example/d.bin"},
			reason:     notStoredSize,
		},
		"session budget exhausted": {
			store:      budgetExhaustedStore(t),
			client:     stubClient(&stubRoundTripper{body: textAttachment}),
			attachment: InputAttachment{ID: "e", Name: "e.txt", URL: "https://cdn.example/e.txt", Size: int64(len(textAttachment))},
			reason:     notStoredSize,
		},
		"no download location": {
			store:      newTestStore(t, 1<<20, 4<<20),
			client:     stubClient(&stubRoundTripper{body: body}),
			attachment: InputAttachment{ID: "f", Name: "f.txt"},
			reason:     notStoredNoURL,
		},
		"unusable download location": {
			store:      newTestStore(t, 1<<20, 4<<20),
			client:     stubClient(&stubRoundTripper{body: body}),
			attachment: InputAttachment{ID: "g", Name: "g.txt", URL: "https://cdn.example/g.txt", ProxyURL: "https://cdn.example/%zz"},
			reason:     notStoredBadURL,
		},
		"location with no scheme": {
			store:      newTestStore(t, 1<<20, 4<<20),
			client:     stubClient(&stubRoundTripper{body: body}),
			attachment: InputAttachment{ID: "i", Name: "i.txt", ProxyURL: "not a url"},
			reason:     notStoredNetwork,
		},
		"cancelled turn": {
			store:      newTestStore(t, 1<<20, 4<<20),
			client:     stubClient(&stubRoundTripper{body: body}),
			attachment: InputAttachment{ID: "h", Name: "h.txt", URL: "https://cdn.example/h.txt"},
			reason:     notStoredCancel,
		},
	} {
		t.Run(name, func(t *testing.T) {
			attachments := &Attachments{Store: test.store, Client: test.client, DownloadTimeout: 2 * time.Second}
			message := attachmentMessage("keep my turn", test.attachment)
			ctx := context.Background()
			if name == "cancelled turn" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			hydrated := attachments.Hydrate(ctx, message)
			if len(hydrated.Stored) != 0 {
				t.Fatalf("nothing should be stored, got %+v", hydrated.Stored)
			}
			if len(hydrated.Problems) != 1 {
				t.Fatalf("problems = %+v", hydrated.Problems)
			}
			if hydrated.Problems[0].Reason != test.reason {
				t.Fatalf("reason = %q, want %q", hydrated.Problems[0].Reason, test.reason)
			}
			// The user's text survives, so the turn is never lost.
			if !strings.HasPrefix(hydrated.Content, "keep my turn") {
				t.Fatalf("content = %q", hydrated.Content)
			}
			// And the note is a safe indicator: no URL, no path, no response body.
			for _, forbidden := range []string{"cdn.example", "http", "secret internal detail", "dial tcp", string(rune(0))} {
				if strings.Contains(hydrated.Content, forbidden) {
					t.Fatalf("reference text leaked %q: %q", forbidden, hydrated.Content)
				}
			}
		})
	}
}

func TestHydrateReportsEachFailureAndKeepsSuccessfulFiles(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	stub := &stubRoundTripper{byPath: map[string][]byte{"/good.txt": textAttachment}}
	attachments := &Attachments{Store: store, Client: stubClient(stub)}
	message := attachmentMessage("mixed",
		InputAttachment{ID: "1", Name: "good.txt", URL: "https://cdn.example/good.txt", Size: int64(len(textAttachment))},
		InputAttachment{ID: "2", Name: "gone.txt", URL: "https://cdn.example/gone.txt"},
	)
	hydrated := attachments.Hydrate(context.Background(), message)
	if len(hydrated.Stored) != 1 || hydrated.Stored[0].Name != "good.txt" {
		t.Fatalf("stored = %+v", hydrated.Stored)
	}
	if len(hydrated.Problems) != 1 || hydrated.Problems[0].Name != "gone.txt" {
		t.Fatalf("problems = %+v", hydrated.Problems)
	}
	notes := AttachmentNotes(hydrated.Stored, hydrated.Problems)
	if strings.Index(notes, "good.txt") > strings.Index(notes, "gone.txt") {
		t.Fatalf("stored files must be listed before failures: %q", notes)
	}
}

func TestHydrateBoundsHostileFilenames(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	attachments := &Attachments{Store: store, Client: stubClient(&stubRoundTripper{body: textAttachment})}
	hostile := "evil\n---\n../../secret " + strings.Repeat("x", 300)
	message := attachmentMessage("", InputAttachment{ID: "1", Name: hostile, URL: "https://cdn.example/x", Size: int64(len(textAttachment))})
	hydrated := attachments.Hydrate(context.Background(), message)
	if len(hydrated.Stored) != 1 {
		t.Fatalf("stored = %+v problems = %+v", hydrated.Stored, hydrated.Problems)
	}
	note := AttachmentNotes(hydrated.Stored, hydrated.Problems)
	if strings.Count(note, "\n") != 0 {
		t.Fatalf("a filename must not add lines to the reference text: %q", note)
	}
	if len([]rune(note)) > 2*maxReferenceRunes+40 {
		t.Fatalf("reference text is unbounded: %d runes", len([]rune(note)))
	}
	if strings.Contains(note, "..") {
		t.Fatalf("path traversal survived: %q", note)
	}
}

func TestHydrateUsesMessageSessionIDAsStoreScope(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	attachments := &Attachments{Store: store, Client: stubClient(&stubRoundTripper{body: textAttachment})}
	message := attachmentMessage("", InputAttachment{ID: "1", Name: "a.txt", URL: "https://cdn.example/a.txt", Size: int64(len(textAttachment))})
	message.SessionID = "discord:channel:other-session"
	hydrated := attachments.Hydrate(context.Background(), message)
	if len(hydrated.Stored) != 1 {
		t.Fatalf("stored = %+v", hydrated.Stored)
	}
	if _, _, err := store.Get(context.Background(), testSession, hydrated.Stored[0].RefID); !errors.Is(err, filestore.ErrNotFound) {
		t.Fatalf("file leaked into another session scope: %v", err)
	}
	if _, _, err := store.Get(context.Background(), "discord:channel:other-session", hydrated.Stored[0].RefID); err != nil {
		t.Fatalf("file is not readable by its own session: %v", err)
	}
}

func TestStoreRejectsForeignSessionReference(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	attachments := &Attachments{Store: store, Client: stubClient(&stubRoundTripper{body: textAttachment})}
	hydrated := attachments.Hydrate(context.Background(), attachmentMessage("", InputAttachment{ID: "1", Name: "a.txt", URL: "https://cdn.example/a.txt", Size: int64(len(textAttachment))}))
	ref := hydrated.Stored[0].RefID
	if _, _, err := store.Get(context.Background(), "discord:channel:victim", ref); err == nil {
		t.Fatal("a reference must not resolve in another session")
	}
}

// ---------------------------------------------------------------------------
// intake: canonical Input metadata contract
// ---------------------------------------------------------------------------

func TestToInputKeepsRoutingMetadataAndAddsReferences(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	attachments := &Attachments{Store: store, Client: stubClient(&stubRoundTripper{body: textAttachment})}
	hydrated := attachments.Hydrate(context.Background(), attachmentMessage("what is in here?",
		InputAttachment{ID: "1", Name: "a.txt", ContentType: "text/plain", URL: "https://cdn.example/a.txt", Size: int64(len(textAttachment))},
		InputAttachment{ID: "2", Name: "b.png", URL: "https://cdn.example/b.png", Size: int64(len(pngBytes))},
	))
	input := ToInput(hydrated)

	// The four original keys are untouched: lifecycle continuation and reply
	// routing depend on them (REQ-021).
	if input.Metadata["channel_id"] != testChannel || input.Metadata["message_id"] != testMessage {
		t.Fatalf("routing metadata changed: %+v", input.Metadata)
	}
	if input.Metadata["author_id"] != testAuthor || input.Metadata["author_name"] != testAuthorNm {
		t.Fatalf("author metadata changed: %+v", input.Metadata)
	}
	if input.SessionID != testSession || input.Source != "discord" {
		t.Fatalf("identity changed: %+v", input)
	}
	// The canonical Turn contract is unchanged in shape: one text part, no
	// binary content, no new part type (REQ-016, CON-003).
	if len(input.Turn.Content) != 1 || input.Turn.Content[0].Type != sdk.ContentText {
		t.Fatalf("turn shape changed: %+v", input.Turn.Content)
	}
	if len(input.Turn.Content[0].Text) > 4096 {
		t.Fatalf("turn text is unbounded: %d bytes", len(input.Turn.Content[0].Text))
	}
	for _, part := range input.Turn.Content {
		if strings.Contains(part.Text, "cdn.example") || strings.Contains(part.Text, "https://") {
			t.Fatalf("a download URL reached the model-visible text: %q", part.Text)
		}
	}

	// Reference schema.
	if got := input.Metadata[MetaAttachmentCount]; got != "2" {
		t.Fatalf("%s = %q", MetaAttachmentCount, got)
	}
	if got := input.Metadata[MetaAttachmentIDs]; got != hydrated.Stored[0].RefID+","+hydrated.Stored[1].RefID {
		t.Fatalf("%s = %q", MetaAttachmentIDs, got)
	}
	for index, stored := range hydrated.Stored {
		if got := input.Metadata[AttachmentKey(index, MetaAttachmentID)]; got != stored.RefID {
			t.Fatalf("attachment_%d_id = %q, want %q", index+1, got, stored.RefID)
		}
		if got := input.Metadata[AttachmentKey(index, MetaAttachmentName)]; got != stored.Name {
			t.Fatalf("attachment_%d_name = %q, want %q", index+1, got, stored.Name)
		}
		if got := input.Metadata[AttachmentKey(index, MetaAttachmentContentType)]; got != stored.ContentType {
			t.Fatalf("attachment_%d_content_type = %q, want %q", index+1, got, stored.ContentType)
		}
		if got := input.Metadata[AttachmentKey(index, MetaAttachmentSize)]; got != fmt.Sprint(stored.Size) {
			t.Fatalf("attachment_%d_size = %q, want %d", index+1, got, stored.Size)
		}
		if got := input.Metadata[AttachmentKey(index, MetaAttachmentPath)]; got != stored.Path {
			t.Fatalf("attachment_%d_path = %q, want %q", index+1, got, stored.Path)
		}
	}
	// Indexes start at 1 and never collide with the original keys.
	if _, ok := input.Metadata["attachment_0_id"]; ok {
		t.Fatal("attachment indexes must start at 1")
	}
	// No failure notes were earned, so the problem keys stay absent.
	if _, ok := input.Metadata[MetaAttachmentProblemCount]; ok {
		t.Fatalf("problem count written for a clean message: %+v", input.Metadata)
	}
	// Round trip: the reader of the schema sees exactly what was stored.
	back := StoredFromMetadata(input.Metadata)
	if len(back) != 2 || back[0].RefID != hydrated.Stored[0].RefID || back[1].Path != hydrated.Stored[1].Path {
		t.Fatalf("StoredFromMetadata = %+v", back)
	}
	// The store ID is opaque: it is not a path and carries no URL.
	for _, key := range []string{MetaAttachmentIDs, AttachmentKey(0, MetaAttachmentPath)} {
		if strings.Contains(input.Metadata[key], "https://") {
			t.Fatalf("%s leaks a URL: %q", key, input.Metadata[key])
		}
	}
}

func TestToInputAttachmentOnlyMessageIsNeverAnEmptyTurn(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	attachments := &Attachments{Store: store, Client: stubClient(&stubRoundTripper{body: textAttachment})}
	hydrated := attachments.Hydrate(context.Background(), attachmentMessage("", InputAttachment{ID: "1", Name: "a.txt", URL: "https://cdn.example/a.txt", Size: int64(len(textAttachment))}))
	input := ToInput(hydrated)
	if strings.TrimSpace(input.Turn.Content[0].Text) == "" {
		t.Fatal("attachment-only message produced an empty turn")
	}
	if _, ok := input.Metadata[MetaAttachmentProblemCount]; ok {
		t.Fatalf("unexpected problems: %+v", hydrated.Problems)
	}
}

func TestToInputRecordsProblemsAlongsideRoutingMetadata(t *testing.T) {
	attachments := &Attachments{}
	hydrated := attachments.Hydrate(context.Background(), attachmentMessage("  ",
		InputAttachment{ID: "1", Name: "a.txt"},
		InputAttachment{ID: "2", Name: "b.png"},
	))
	input := ToInput(hydrated)
	if got := input.Metadata[MetaAttachmentProblemCount]; got != "2" {
		t.Fatalf("%s = %q", MetaAttachmentProblemCount, got)
	}
	if got := input.Metadata[MetaAttachmentProblems]; got != "a.txt,b.png" {
		t.Fatalf("%s = %q", MetaAttachmentProblems, got)
	}
	if _, ok := input.Metadata[MetaAttachmentIDs]; ok {
		t.Fatal("a message with nothing stored must not claim references")
	}
	if input.Metadata["channel_id"] != testChannel || input.Metadata["message_id"] != testMessage {
		t.Fatalf("routing metadata lost on the failure path: %+v", input.Metadata)
	}
	if strings.TrimSpace(input.Turn.Content[0].Text) == "" {
		t.Fatal("every file failed and the turn became empty")
	}
	if back := StoredFromMetadata(input.Metadata); back != nil {
		t.Fatalf("StoredFromMetadata invented files: %+v", back)
	}
}

func TestToInputWithoutAttachmentsKeepsExactOriginalMetadata(t *testing.T) {
	input := ToInput(attachmentMessage("hello"))
	want := map[string]string{"channel_id": testChannel, "message_id": testMessage, "author_id": testAuthor, "author_name": testAuthorNm}
	if len(input.Metadata) != len(want) {
		t.Fatalf("metadata = %+v, want exactly the four original keys", input.Metadata)
	}
	for key, value := range want {
		if input.Metadata[key] != value {
			t.Fatalf("%s = %q, want %q", key, input.Metadata[key], value)
		}
	}
}

func TestStoredFromMetadataIsDefensive(t *testing.T) {
	if got := StoredFromMetadata(nil); got != nil {
		t.Fatalf("nil metadata = %+v", got)
	}
	if got := StoredFromMetadata(map[string]string{"attachment_count": "nonsense"}); got != nil {
		t.Fatalf("garbage count = %+v", got)
	}
	if got := StoredFromMetadata(map[string]string{"attachment_count": "-3"}); got != nil {
		t.Fatalf("negative count = %+v", got)
	}
	// A count above the loop bound cannot be padded into a huge allocation, and
	// a stray key with no matching index is ignored.
	huge := map[string]string{"attachment_count": fmt.Sprint(1 << 20), "attachment_1_name": "orphan.txt"}
	if got := StoredFromMetadata(huge); len(got) != 0 {
		t.Fatalf("orphan keys produced %d records", len(got))
	}
	// A missing id in the middle does not shift the later entries.
	gappy := map[string]string{
		MetaAttachmentCount:                "3",
		AttachmentKey(0, MetaAttachmentID): "aaaaaaaaaaaaaaaaaaaaaa",
		AttachmentKey(2, MetaAttachmentID): "bbbbbbbbbbbbbbbbbbbbbb",
	}
	got := StoredFromMetadata(gappy)
	if len(got) != 2 || got[1].RefID != "bbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("gappy = %+v", got)
	}
}

func TestAttachmentKeyFormatting(t *testing.T) {
	if got := AttachmentKey(0, MetaAttachmentID); got != "attachment_1_id" {
		t.Fatalf("AttachmentKey(0) = %q", got)
	}
	if got := AttachmentKey(11, MetaAttachmentContentType); got != "attachment_12_content_type" {
		t.Fatalf("AttachmentKey(11) = %q", got)
	}
}

func TestFormatBytesAndDisplayNames(t *testing.T) {
	for size, want := range map[int64]string{-5: "0 B", 0: "0 B", 512: "512 B", 1023: "1023 B", 1024: "1.0 KiB", 1<<20 + 1024: "1.0 MiB", 1 << 30: "1.0 GiB", 1 << 40: "1.0 TiB"} {
		if got := formatBytes(size); got != want {
			t.Fatalf("formatBytes(%d) = %q, want %q", size, got, want)
		}
	}
	if got := parseSize(" 12 "); got != 12 {
		t.Fatalf("parseSize = %d", got)
	}
	if got := parseSize("junk"); got != 0 {
		t.Fatalf("parseSize(junk) = %d", got)
	}
	if got := displayName(strings.Repeat("z", 400)); len([]rune(got)) != maxReferenceRunes {
		t.Fatalf("displayName length = %d", len([]rune(got)))
	}
	if got := displayName(""); got != "attachment" {
		t.Fatalf("displayName(empty) = %q", got)
	}
}

// ---------------------------------------------------------------------------
// intake: the InputSource pipeline
// ---------------------------------------------------------------------------

func TestInputSourceHydratesBeforeEmitting(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	attachments := &Attachments{Store: store, Client: stubClient(&stubRoundTripper{body: textAttachment})}
	messages := make(chan InputMessage, 2)
	messages <- attachmentMessage("file please", InputAttachment{ID: "1", Name: "a.txt", URL: "https://cdn.example/a.txt", Size: int64(len(textAttachment))})
	messages <- attachmentMessage("no files")
	close(messages)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inputs, err := InputSource{Messages: messages, Attachments: attachments}.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first := <-inputs
	if first.Metadata[MetaAttachmentCount] != "1" {
		t.Fatalf("first input metadata = %+v", first.Metadata)
	}
	if !strings.Contains(first.Turn.Content[0].Text, "[attached: a.txt") {
		t.Fatalf("reference text missing: %q", first.Turn.Content[0].Text)
	}
	second := <-inputs
	if len(second.Metadata) != 4 {
		t.Fatalf("file-free turn gained metadata: %+v", second.Metadata)
	}
	if _, ok := <-inputs; ok {
		t.Fatal("channel must close after the messages run out")
	}
}

func TestInputSourceWithoutAttachmentsStillWorks(t *testing.T) {
	messages := make(chan InputMessage, 1)
	messages <- attachmentMessage("plain", InputAttachment{ID: "1", Name: "a.txt"})
	close(messages)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inputs, err := InputSource{Messages: messages}.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := <-inputs
	// A runtime with no file pipeline records why nothing arrived, and keeps the
	// user's text.
	if input.Metadata[MetaAttachmentProblemCount] != "1" {
		t.Fatalf("metadata = %+v", input.Metadata)
	}
	if !strings.Contains(input.Turn.Content[0].Text, "not stored; attachment storage is disabled") {
		t.Fatalf("text = %q", input.Turn.Content[0].Text)
	}
}

func TestDownloadTimeoutBoundsASlowServer(t *testing.T) {
	// A stalled CDN must not hold a turn open: the transport's own budget expires
	// the request and the file is reported as unavailable.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	store := newTestStore(t, 1<<20, 4<<20)
	attachments := &Attachments{Store: store, Client: server.Client(), DownloadTimeout: 150 * time.Millisecond}
	start := time.Now()
	hydrated := attachments.Hydrate(context.Background(), attachmentMessage("", InputAttachment{ID: "1", Name: "slow.txt", URL: server.URL + "/slow"}))
	if time.Since(start) > 3*time.Second {
		t.Fatalf("hydrate took %s, the download timeout did not bound it", time.Since(start))
	}
	if len(hydrated.Stored) != 0 || len(hydrated.Problems) != 1 {
		t.Fatalf("stored=%+v problems=%+v", hydrated.Stored, hydrated.Problems)
	}
	if hydrated.Problems[0].Reason != notStoredNetwork {
		t.Fatalf("reason = %q", hydrated.Problems[0].Reason)
	}
}

func TestAttachmentFileConfigAppliesOnlyPositiveValues(t *testing.T) {
	attachments := &Attachments{}
	(&AttachmentFileConfig{}).apply(attachments)
	if attachments.DownloadTimeout != 0 || attachments.UploadTimeout != 0 || attachments.MaxSendFileBytes != 0 || attachments.MaxSendFileCount != 0 {
		t.Fatalf("a zero config must leave the defaults alone: %+v", attachments)
	}
	(&AttachmentFileConfig{DownloadTimeout: 3 * time.Second, UploadTimeout: 4 * time.Minute, MaxSendFileBytes: 99, MaxSendFileCount: 2}).apply(attachments)
	if attachments.downloadTimeout() != 3*time.Second || attachments.uploadTimeout() != 4*time.Minute {
		t.Fatalf("timeouts = %v/%v", attachments.downloadTimeout(), attachments.uploadTimeout())
	}
	if attachments.sendFileBytes() != 99 || attachments.sendFileCount() != 2 {
		t.Fatalf("send bounds = %d/%d", attachments.sendFileBytes(), attachments.sendFileCount())
	}
	// An unset pipeline still has bounded transport defaults.
	var none *Attachments
	if none.downloadTimeout() != defaultDownloadTimeout || none.uploadTimeout() != defaultUploadTimeout || none.sendFileBytes() != defaultSendFileBytes || none.sendFileCount() != defaultSendFileCount {
		t.Fatal("nil attachment defaults drifted")
	}
}

func TestAttachmentsNilStoreIsInert(t *testing.T) {
	if (&Attachments{}).enabled() {
		t.Fatal("an attachments with no store must be disabled")
	}
	var none *Attachments
	if none.enabled() {
		t.Fatal("nil attachments must be disabled")
	}
}

// ---------------------------------------------------------------------------
// outbound: reference resolution and upload
// ---------------------------------------------------------------------------

// uploadRecorder is a Sender that also implements fileSender, so the outbound
// path can be observed without any REST traffic.
type uploadRecorder struct {
	texts  []string
	upload []uploadCall
	err    error
}

type uploadCall struct {
	channelID string
	content   string
	files     []fileRecord
}

type fileRecord struct {
	Name        string
	ContentType string
	Body        []byte
}

func (u *uploadRecorder) SendMessage(_ context.Context, channelID, content string) error {
	u.texts = append(u.texts, channelID+"|"+content)
	return nil
}

// textOnlyRecorder is a Sender without any upload capability: it records every
// text message, so the note that a refused upload must produce is observable
// even though the response text was delivered first.
type textOnlyRecorder struct{ texts []string }

func (r *textOnlyRecorder) SendMessage(_ context.Context, channelID, content string) error {
	r.texts = append(r.texts, channelID+"|"+content)
	return nil
}

func (u *uploadRecorder) SendFiles(_ context.Context, channelID, content string, files []*discordgo.File) error {
	call := uploadCall{channelID: channelID, content: content}
	for _, file := range files {
		body, err := io.ReadAll(file.Reader)
		if err != nil {
			return err
		}
		call.files = append(call.files, fileRecord{Name: file.Name, ContentType: file.ContentType, Body: body})
	}
	u.upload = append(u.upload, call)
	return u.err
}

func outboundOutput(mode, ids, session string) sdk.Output {
	metadata := map[string]string{"channel_id": testChannel}
	if ids != "" {
		metadata[MetaOutAttachmentIDs] = ids
	}
	if mode != "" {
		metadata[MetaOutAttachments] = mode
	}
	return sdk.Output{Source: "discord", SessionID: session, Metadata: metadata, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "here you go"}}}
}

// outboundRequestOutput names explicit references with no mode, which is the
// narrowest statement of intent an output can make.
func outboundRequestOutput(ids []string) sdk.Output {
	return outboundOutput("", strings.Join(ids, ","), testSession)
}

func storedOne(t *testing.T, store *filestore.Store, session, name string, body []byte) string {
	t.Helper()
	ref, err := store.Put(context.Background(), session, name, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("seed store: %v", err)
	}
	return ref.ID
}

func TestOutboundUploadsResolvedReference(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	ref := storedOne(t, store, testSession, "report.txt", textAttachment)
	sender := &uploadRecorder{}
	display := Display{Sender: sender, Attachments: &Attachments{Store: store}}

	if err := display.Display(context.Background(), outboundOutput("", ref, testSession)); err != nil {
		t.Fatal(err)
	}
	if len(sender.texts) != 1 || !strings.HasSuffix(sender.texts[0], "|here you go") {
		t.Fatalf("text must be delivered first and unchanged: %q", sender.texts)
	}
	if len(sender.upload) != 1 {
		t.Fatalf("uploads = %d", len(sender.upload))
	}
	call := sender.upload[0]
	if call.channelID != testChannel {
		t.Fatalf("channel = %q", call.channelID)
	}
	if len(call.files) != 1 {
		t.Fatalf("files = %+v", call.files)
	}
	if call.files[0].Name != "report.txt" {
		t.Fatalf("file name = %q", call.files[0].Name)
	}
	if !bytes.Equal(call.files[0].Body, textAttachment) {
		t.Fatalf("uploaded body = %q", call.files[0].Body)
	}
	if !strings.HasPrefix(call.files[0].ContentType, "text/plain") {
		t.Fatalf("content type = %q", call.files[0].ContentType)
	}
}

func TestOutboundUploadsEveryNamedReferenceInOrder(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	first := storedOne(t, store, testSession, "one.txt", []byte("first file"))
	second := storedOne(t, store, testSession, "two.png", pngBytes)
	sender := &uploadRecorder{}
	display := Display{Sender: sender, Attachments: &Attachments{Store: store}}

	if err := display.Display(context.Background(), outboundOutput("", second+","+first, testSession)); err != nil {
		t.Fatal(err)
	}
	files := sender.upload[0].files
	if len(files) != 2 || files[0].Name != "two.png" || files[1].Name != "one.txt" {
		t.Fatalf("files = %+v, want the requested order", files)
	}
	if !bytes.Equal(files[0].Body, pngBytes) {
		t.Fatal("PNG bytes were not uploaded verbatim")
	}
}

func TestOutboundManifestAndLatestModes(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	older := storedOne(t, store, testSession, "older.txt", []byte("aaaaaaaa"))
	time.Sleep(2 * time.Millisecond)
	newer := storedOne(t, store, testSession, "newer.txt", []byte("bbbbbbbb"))
	// The seeds are what "latest" and "manifest" are expected to name, so the
	// mode results are checked against the real references, not only names.
	newerSender := &uploadRecorder{}
	newerDisplay := Display{Sender: newerSender, Attachments: &Attachments{Store: store}}
	if err := newerDisplay.Display(context.Background(), outboundRequestOutput([]string{newer, older})); err != nil {
		t.Fatal(err)
	}
	if got := newerSender.upload[0].files; len(got) != 2 || got[0].Name != "newer.txt" || got[1].Name != "older.txt" {
		t.Fatalf("explicit ID order = %+v, want the requested order", got)
	}
	latestSender := &uploadRecorder{}
	latestDisplay := Display{Sender: latestSender, Attachments: &Attachments{Store: store}}
	if err := latestDisplay.Display(context.Background(), outboundOutput(OutboundModeLatest, "", testSession)); err != nil {
		t.Fatal(err)
	}
	if got := latestSender.upload[0].files; len(got) != 1 || got[0].Name != "newer.txt" {
		t.Fatalf("latest mode files = %+v", got)
	}

	manifestSender := &uploadRecorder{}
	manifestDisplay := Display{Sender: manifestSender, Attachments: &Attachments{Store: store}}
	if err := manifestDisplay.Display(context.Background(), outboundOutput(OutboundModeManifest, "", testSession)); err != nil {
		t.Fatal(err)
	}
	if got := manifestSender.upload[0].files; len(got) != 2 || got[0].Name != "older.txt" || got[1].Name != "newer.txt" {
		t.Fatalf("manifest mode files = %+v, want both oldest first", got)
	}

	// A per-message cap truncates the manifest and says so instead of failing.
	cappedSender := &uploadRecorder{}
	capped := Display{Sender: cappedSender, Attachments: &Attachments{Store: store, MaxSendFileCount: 1}}
	if err := capped.Display(context.Background(), outboundOutput(OutboundModeManifest, "", testSession)); err != nil {
		t.Fatal(err)
	}
	if got := cappedSender.upload[0].files; len(got) != 1 {
		t.Fatalf("capped files = %+v", got)
	}
	if !strings.Contains(cappedSender.upload[0].content, outboundCountNote) {
		t.Fatalf("count note missing: %q", cappedSender.upload[0].content)
	}
	// Explicit IDs are capped too.
	idSender := &uploadRecorder{}
	idDisplay := Display{Sender: idSender, Attachments: &Attachments{Store: store, MaxSendFileCount: 1}}
	if err := idDisplay.Display(context.Background(), outboundOutput("", "junk,junk2", testSession)); err != nil {
		t.Fatal(err)
	}
	if len(idSender.upload) != 0 || len(idSender.texts) != 2 {
		t.Fatalf("uploads=%d texts=%q", len(idSender.upload), idSender.texts)
	}
}

func TestOutboundManifestIsScopedToTheOutputsSession(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	foreign := "discord:channel:victim"
	mine := storedOne(t, store, testSession, "mine.txt", []byte("mine"))
	theirs := storedOne(t, store, foreign, "victim.txt", []byte("not yours"))
	sender := &uploadRecorder{}
	display := Display{Sender: sender, Attachments: &Attachments{Store: store}}

	// Naming another session's reference must not deliver it, and must not be
	// echoed either: the note says only that a requested file is not an
	// attachment of this session.
	if err := display.Display(context.Background(), outboundOutput("", mine+","+theirs, testSession)); err != nil {
		t.Fatal(err)
	}
	if len(sender.upload) != 1 {
		t.Fatalf("uploads = %d, want one", len(sender.upload))
	}
	files := sender.upload[0].files
	if len(files) != 1 || files[0].Name != "mine.txt" {
		t.Fatalf("files = %+v, want only this session's file", files)
	}
	if !strings.Contains(sender.upload[0].content, outboundMissingNote) {
		t.Fatalf("foreign reference was ignored silently: %q", sender.upload[0].content)
	}
	for _, text := range append(sender.texts, sender.upload[0].content) {
		if strings.Contains(text, theirs) || strings.Contains(text, "victim.txt") || strings.Contains(text, foreign) {
			t.Fatalf("another session's identity leaked into a channel: %q", text)
		}
	}
	// "manifest" sees only the output's own session, so a foreign file cannot be
	// reached by name at all.
	manifestSender := &uploadRecorder{}
	manifestDisplay := Display{Sender: manifestSender, Attachments: &Attachments{Store: store}}
	if err := manifestDisplay.Display(context.Background(), outboundOutput(OutboundModeManifest, "", testSession)); err != nil {
		t.Fatal(err)
	}
	if got := manifestSender.upload[0].files; len(got) != 1 || got[0].Name != "mine.txt" {
		t.Fatalf("manifest files = %+v, want this session only", got)
	}
}

func TestOutboundReportsFailuresInsteadOfDroppingThem(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	ref := storedOne(t, store, testSession, "ok.txt", textAttachment)
	for name, test := range map[string]struct {
		attachments *Attachments
		sender      Sender
		output      sdk.Output
		want        string
	}{
		"no upload capability": {
			attachments: &Attachments{Store: store},
			sender:      &textOnlyRecorder{},
			output:      outboundOutput("", ref, testSession),
			want:        outboundUnsupportedNote,
		},
		"storage disabled": {
			attachments: nil,
			sender:      &uploadRecorder{},
			output:      outboundOutput(OutboundModeManifest, "", testSession),
			want:        outboundDisabledNote,
		},
		"no session": {
			attachments: &Attachments{Store: store},
			sender:      &uploadRecorder{},
			output:      outboundOutput("", ref, ""),
			want:        outboundNoSessionNote,
		},
		"unknown reference": {
			attachments: &Attachments{Store: store},
			sender:      &uploadRecorder{},
			output:      outboundOutput("", "not-a-reference", testSession),
			want:        outboundMissingNote,
		},
		"inline bytes refused": {
			attachments: &Attachments{Store: store},
			sender:      &uploadRecorder{},
			output: func() sdk.Output {
				output := outboundOutput("", "", testSession)
				output.Metadata[MetaOutAttachmentRaw] = "data:;base64,AAAA"
				return output
			}(),
			want: outboundInlineNote,
		},
		"unrecognised mode refused": {
			attachments: &Attachments{Store: store},
			sender:      &uploadRecorder{},
			output:      outboundOutput("everything", "", testSession),
			want:        outboundInlineNote,
		},
		"file larger than the send limit": {
			attachments: &Attachments{Store: store, MaxSendFileBytes: 4},
			sender:      &uploadRecorder{},
			output:      outboundOutput("", ref, testSession),
			want:        "was not sent; it is larger than the send size limit",
		},
	} {
		t.Run(name, func(t *testing.T) {
			display := Display{Sender: test.sender, Attachments: test.attachments}
			if err := display.Display(context.Background(), test.output); err != nil {
				t.Fatal(err)
			}
			var texts []string
			switch sender := test.sender.(type) {
			case *uploadRecorder:
				if len(sender.upload) != 0 {
					t.Fatalf("nothing should be uploaded, got %+v", sender.upload)
				}
				texts = sender.texts
			case *textOnlyRecorder:
				texts = sender.texts
			default:
				t.Fatalf("unexpected sender %T", test.sender)
			}
			joined := strings.Join(texts, "\n")
			if !strings.Contains(joined, test.want) {
				t.Fatalf("channel output %q must contain %q", joined, test.want)
			}
			// The readable answer is still delivered before the failure note.
			if !strings.Contains(joined, "here you go") {
				t.Fatalf("response text was lost: %q", joined)
			}
			for _, forbidden := range []string{"AAAA", "data:", "base64", "http", "/tmp", filestore.DefaultRoot, "everything"} {
				if strings.Contains(joined, forbidden) {
					t.Fatalf("channel output leaked %q: %q", forbidden, joined)
				}
			}
		})
	}
}

func TestOutboundWithoutFileRequestIsUnchanged(t *testing.T) {
	sender := &uploadRecorder{}
	display := Display{Sender: sender, Attachments: &Attachments{Store: newTestStore(t, 1<<20, 4<<20)}}
	if err := display.Display(context.Background(), outboundOutput("", "", testSession)); err != nil {
		t.Fatal(err)
	}
	if len(sender.upload) != 0 {
		t.Fatalf("unexpected upload: %+v", sender.upload)
	}
	if len(sender.texts) != 1 || sender.texts[0] != testChannel+"|here you go" {
		t.Fatalf("plain text path changed: %q", sender.texts)
	}
}

func TestOutboundUploadFailureStaysSafe(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	ref := storedOne(t, store, testSession, "a.txt", textAttachment)
	sender := &uploadRecorder{err: errors.New(`403: {"message":"Invalid Form Body"} https://discord.com/api/v10/secret`)}
	display := Display{Sender: sender, Attachments: &Attachments{Store: store}}
	err := display.Display(context.Background(), outboundOutput("", ref, testSession))
	if err == nil {
		t.Fatal("an upload failure must be reported to the caller")
	}
	joined := strings.Join(sender.texts, "\n")
	if !strings.Contains(joined, outboundSendFailureNote) {
		t.Fatalf("no safe failure indicator: %q", joined)
	}
	for _, forbidden := range []string{"discord.com", "Invalid Form Body", "403", ref} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("raw REST detail reached the channel (%q): %q", forbidden, joined)
		}
	}
}

func TestOutboundDeduplicatesWithinOneTurn(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	ref := storedOne(t, store, testSession, "a.txt", textAttachment)
	attachments := &Attachments{Store: store}
	sender := &uploadRecorder{}
	display := Display{Sender: sender, Attachments: attachments}
	output := outboundOutput("", ref, testSession)
	for i := 0; i < 3; i++ {
		if err := display.Display(context.Background(), output); err != nil {
			t.Fatal(err)
		}
	}
	if len(sender.upload) != 1 {
		t.Fatalf("uploads = %d, want one per turn", len(sender.upload))
	}
	// A new turn on the channel clears the record, so the same reference can be
	// sent again later.
	attachments.forgetSent(testChannel)
	if err := display.Display(context.Background(), output); err != nil {
		t.Fatal(err)
	}
	if len(sender.upload) != 2 {
		t.Fatalf("uploads = %d after a new turn", len(sender.upload))
	}
	// A different request inside the same turn is still honoured.
	if err := display.Display(context.Background(), outboundOutput(OutboundModeManifest, "", testSession)); err != nil {
		t.Fatal(err)
	}
	if len(sender.upload) != 3 {
		t.Fatalf("uploads = %d, want the manifest request treated separately", len(sender.upload))
	}
}

func TestOutboundRunsOnTheTransportUploadBudget(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	ref := storedOne(t, store, testSession, "a.txt", textAttachment)
	sender := &uploadRecorder{}
	display := Display{Sender: sender, Attachments: &Attachments{Store: store, UploadTimeout: time.Minute}}
	// A parent that is already past the harness display timeout must not kill the
	// upload: the transport derives its own context.
	expired, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := display.Display(expired, outboundOutput("", ref, testSession)); err != nil {
		t.Fatal(err)
	}
	if len(sender.upload) != 1 {
		t.Fatalf("uploads = %d, want the file delivered anyway", len(sender.upload))
	}
}

func TestOutboundCancelledUploadIsReported(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	ref := storedOne(t, store, testSession, "a.txt", textAttachment)
	sender := &uploadRecorder{}
	display := Display{Sender: sender, Attachments: &Attachments{Store: store, UploadTimeout: time.Minute}}
	// The store is reached through a context that expires immediately, so every
	// lookup fails; the turn must report it and still keep its text.
	attachments := display.attachments()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	files, notes := attachments.resolveOutbound(cancelled, testSession, outboundRequest{ids: []string{ref}})
	for _, file := range files {
		file.close()
	}
	if len(files) != 0 {
		t.Fatalf("files = %d on a cancelled context", len(files))
	}
	if strings.Join(notes, "\n") == "" {
		t.Fatal("a cancelled lookup must be reported")
	}
}

func TestSentKeyDistinguishesRequests(t *testing.T) {
	ids := sentKey(outboundRequest{ids: []string{"a", "b"}})
	if ids != "a,b" {
		t.Fatalf("sentKey = %q", ids)
	}
	if sentKey(outboundRequest{manifest: true, ids: []string{"a"}}) == ids {
		t.Fatal("a manifest request must not collide with the same explicit IDs")
	}
	if sentKey(outboundRequest{latest: true}) == sentKey(outboundRequest{raw: "x"}) {
		t.Fatal("latest and raw requests must not collide")
	}
}

func TestOutboundRequestedParsesMetadata(t *testing.T) {
	for name, test := range map[string]struct {
		metadata  map[string]string
		requested bool
		request   outboundRequest
	}{
		"nil":          {metadata: nil, requested: false},
		"empty":        {metadata: map[string]string{}, requested: false},
		"blank ids":    {metadata: map[string]string{MetaOutAttachmentIDs: " , , "}, requested: false},
		"one id":       {metadata: map[string]string{MetaOutAttachmentIDs: " a , b "}, requested: true, request: outboundRequest{ids: []string{"a", "b"}}},
		"manifest":     {metadata: map[string]string{MetaOutAttachments: "MANIFEST"}, requested: true, request: outboundRequest{manifest: true}},
		"latest":       {metadata: map[string]string{MetaOutAttachments: " latest "}, requested: true, request: outboundRequest{latest: true}},
		"unknown mode": {metadata: map[string]string{MetaOutAttachments: "all"}, requested: true, request: outboundRequest{raw: "all"}},
		"ids beat raw": {metadata: map[string]string{MetaOutAttachmentRaw: "zz", MetaOutAttachmentIDs: "id"}, requested: true, request: outboundRequest{ids: []string{"id"}}},
		"raw only":     {metadata: map[string]string{MetaOutAttachmentRaw: "zz"}, requested: true, request: outboundRequest{raw: "zz"}},
	} {
		t.Run(name, func(t *testing.T) {
			got, requested := outboundRequested(test.metadata)
			if requested != test.requested {
				t.Fatalf("requested = %v, want %v", requested, test.requested)
			}
			if fmt.Sprint(got) != fmt.Sprint(test.request) {
				t.Fatalf("request = %+v, want %+v", got, test.request)
			}
		})
	}
}

func TestSplitList(t *testing.T) {
	if got := splitList(""); len(got) != 0 {
		t.Fatalf("splitList(empty) = %q", got)
	}
	if got := splitList("a,,b , "); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("splitList = %q", got)
	}
}

func TestResolveOutboundClosesNothingOnEmptySession(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	attachments := &Attachments{Store: store}
	files, notes := attachments.resolveOutbound(context.Background(), "   ", outboundRequest{manifest: true})
	if len(files) != 0 {
		t.Fatalf("files = %d", len(files))
	}
	if strings.Join(notes, "") != outboundNoSessionNote {
		t.Fatalf("notes = %q", notes)
	}
}

func TestNewestRefFallsBackToLastEntry(t *testing.T) {
	if newestRef(nil) != nil {
		t.Fatal("empty manifest has no newest file")
	}
	now := time.Now()
	metas := []filestore.Meta{
		{ID: "a", Name: "a", CreatedAt: now},
		{ID: "b", Name: "b", CreatedAt: now},
		{ID: "c", Name: "c", CreatedAt: now},
	}
	if got := newestRef(metas); got.ID != "c" {
		t.Fatalf("indistinct times should keep delivery order, got %q", got.ID)
	}
	metas[0].CreatedAt = now.Add(time.Hour)
	if got := newestRef(metas); got.ID != "a" {
		t.Fatalf("newest recorded time should win, got %q", got.ID)
	}
}

// ---------------------------------------------------------------------------
// outbound: the real Discord wire format
// ---------------------------------------------------------------------------

// multipartRecorder stands in for Discord's REST endpoint so Gateway.SendFiles
// is exercised against the real discordgo call, including MessageSend.Files.
// It records both request shapes the transport produces on the wire: the JSON
// POST of an ordinary message and the multipart POST of an upload, because
// Display sends text first and files second.
type multipartRecorder struct {
	calls     []multipartCall
	status    int
	failParse bool
}

type multipartCall struct {
	method    string
	path      string
	files     []fileRecord
	content   string
	multipart bool
}

func (m *multipartRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	call := multipartCall{method: req.Method, path: req.URL.Path}
	if req.Method == http.MethodPost {
		mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
		switch {
		case err != nil:
			m.failParse = true
			return nil, fmt.Errorf("unusable content type %q (%v)", req.Header.Get("Content-Type"), err)
		case strings.HasPrefix(mediaType, "multipart/"):
			call.multipart = true
			reader := multipart.NewReader(req.Body, params["boundary"])
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					return nil, err
				}
				body, err := io.ReadAll(part)
				if err != nil {
					return nil, err
				}
				switch name := part.FormName(); name {
				case "payload_json":
					var payload struct {
						Content string `json:"content"`
					}
					if err := json.Unmarshal(body, &payload); err != nil {
						return nil, err
					}
					call.content = payload.Content
				default:
					if !strings.HasPrefix(name, "files[") {
						return nil, fmt.Errorf("unexpected form field %q", name)
					}
					call.files = append(call.files, fileRecord{Name: part.FileName(), ContentType: part.Header.Get("Content-Type"), Body: body})
				}
			}
		default:
			// A plain JSON message: it must carry no files, which is what keeps
			// the text-only path byte-for-byte what it was before attachments.
			var payload struct {
				Content string `json:"content"`
			}
			if req.Body != nil {
				if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
					return nil, err
				}
			}
			call.content = payload.Content
		}
	}
	m.calls = append(m.calls, call)
	status := m.status
	if status == 0 {
		status = http.StatusOK
	}
	if status != http.StatusOK {
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":50035,"message":"upload rejected"}`)), Request: req}, nil
	}
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"sent-1","attachments":[{"id":"att-9","filename":"report.txt"}]}`)), Request: req}, nil
}

func gatewayWithREST(t *testing.T, rt http.RoundTripper) *Gateway {
	t.Helper()
	session, err := discordgo.New("Bot mock")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = &http.Client{Transport: rt}
	session.Ratelimiter = discordgo.NewRatelimiter()
	return &Gateway{session: session, raw: make(chan InputMessage, inboundBuffer), messages: make(chan InputMessage, inboundBuffer), done: make(chan struct{}), retryStatus: map[string]string{}, toolTrace: map[string]*toolTraceState{}}
}

func TestGatewaySendFilesUsesChannelMessageSendComplexWithFiles(t *testing.T) {
	recorder := &multipartRecorder{}
	gateway := gatewayWithREST(t, recorder)
	file := &discordgo.File{Name: "report.txt", ContentType: "text/plain", Reader: strings.NewReader("file body")}
	if err := gateway.SendFiles(context.Background(), testChannel, "📎 1 file(s) attached", []*discordgo.File{file}); err != nil {
		t.Fatal(err)
	}
	if recorder.failParse {
		t.Fatal("the upload was not multipart")
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("calls = %d", len(recorder.calls))
	}
	call := recorder.calls[0]
	if call.method != http.MethodPost {
		t.Fatalf("method = %s", call.method)
	}
	if !strings.HasSuffix(call.path, "/channels/"+testChannel+"/messages") {
		t.Fatalf("path = %q", call.path)
	}
	if call.content != "📎 1 file(s) attached" {
		t.Fatalf("content = %q", call.content)
	}
	if len(call.files) != 1 || call.files[0].Name != "report.txt" || string(call.files[0].Body) != "file body" {
		t.Fatalf("files = %+v", call.files)
	}
	if call.files[0].ContentType != "text/plain" {
		t.Fatalf("part content type = %q", call.files[0].ContentType)
	}
}

func TestGatewaySendFilesValidationAndRestErrors(t *testing.T) {
	file := func() *discordgo.File {
		return &discordgo.File{Name: "a.txt", Reader: strings.NewReader("a")}
	}
	if err := (&Gateway{}).SendFiles(context.Background(), testChannel, "", []*discordgo.File{file()}); err == nil {
		t.Fatal("an uninitialized gateway must refuse")
	}
	gateway := gatewayWithREST(t, &multipartRecorder{})
	if err := gateway.SendFiles(context.Background(), "", "x", []*discordgo.File{file()}); err == nil {
		t.Fatal("a missing channel must be refused")
	}
	if err := gateway.SendFiles(context.Background(), testChannel, "x", nil); err == nil {
		t.Fatal("an empty file list must be refused rather than sending a text message")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := gateway.SendFiles(cancelled, testChannel, "x", []*discordgo.File{file()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled upload error = %v", err)
	}
	// A REST rejection is returned verbatim to the caller, which is the only place
	// the transport may keep it; Display turns it into a safe indicator.
	failing := gatewayWithREST(t, &multipartRecorder{status: http.StatusBadRequest})
	err := failing.SendFiles(context.Background(), testChannel, "x", []*discordgo.File{file()})
	if err == nil {
		t.Fatal("expected a REST error")
	}
	if !strings.Contains(err.Error(), "50035") {
		t.Fatalf("error = %v", err)
	}
}

func TestDisplayUploadsThroughGatewaySendFiles(t *testing.T) {
	// End-to-end inside the transport, still offline: a real store, a real
	// discordgo session writing to a recording RoundTripper.
	store := newTestStore(t, 1<<20, 4<<20)
	ref := storedOne(t, store, testSession, "design.png", pngBytes)
	recorder := &multipartRecorder{}
	gateway := gatewayWithREST(t, recorder)
	gateway.ConfigureAttachments(store, stubClient(&stubRoundTripper{}), AttachmentFileConfig{UploadTimeout: 30 * time.Second})

	output := outboundOutput("", ref, testSession)
	output.Content = []sdk.ContentPart{{Type: sdk.ContentText, Text: "rendered"}}
	if err := gateway.Display(context.Background(), output); err != nil {
		t.Fatal(err)
	}
	if len(recorder.calls) != 2 {
		t.Fatalf("REST calls = %d, want the text message then the upload: %+v", len(recorder.calls), recorder.calls)
	}
	if recorder.calls[0].content != "rendered" || len(recorder.calls[0].files) != 0 {
		t.Fatalf("text message = %+v", recorder.calls[0])
	}
	upload := recorder.calls[1]
	if len(upload.files) != 1 || upload.files[0].Name != "design.png" || !bytes.Equal(upload.files[0].Body, pngBytes) {
		t.Fatalf("upload = %+v", upload)
	}
	if upload.files[0].ContentType != "image/png" {
		t.Fatalf("uploaded content type = %q", upload.files[0].ContentType)
	}
}

// ---------------------------------------------------------------------------
// gateway intake pump
// ---------------------------------------------------------------------------

func TestGatewayConfigureAttachmentsDisablesCleanly(t *testing.T) {
	gateway := gatewayWithREST(t, &multipartRecorder{})
	gateway.ConfigureAttachments(nil, nil, AttachmentFileConfig{})
	if gateway.attachments == nil {
		t.Fatal("the pipeline must always exist so the gateway never nil-checks it")
	}
	if gateway.attachments.enabled() {
		t.Fatal("a nil store must disable both directions")
	}
	// Defaults still apply, so a later request reports a bounded failure.
	if gateway.attachments.downloadTimeout() != defaultDownloadTimeout {
		t.Fatalf("download timeout = %v", gateway.attachments.downloadTimeout())
	}
	gateway.ConfigureAttachments(newTestStore(t, 1<<20, 1<<20), stubClient(&stubRoundTripper{body: textAttachment}), AttachmentFileConfig{DownloadTimeout: time.Second, MaxSendFileCount: 3})
	if !gateway.attachments.enabled() || gateway.attachments.downloadTimeout() != time.Second || gateway.attachments.sendFileCount() != 3 {
		t.Fatalf("configuration did not land: %+v", gateway.attachments)
	}
	if (*Gateway)(nil).ConfigureAttachments(nil, nil, AttachmentFileConfig{}); false {
		t.Fatal("unreachable")
	}
}

func TestPumpIntakeHydratesAndPreservesOrder(t *testing.T) {
	store := newTestStore(t, 1<<20, 4<<20)
	stub := &stubRoundTripper{byPath: map[string][]byte{"/one.txt": []byte("first file"), "/two.txt": []byte("second file")}}
	gateway := gatewayWithREST(t, &multipartRecorder{})
	gateway.ConfigureAttachments(store, stubClient(stub), AttachmentFileConfig{DownloadTimeout: 2 * time.Second})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gateway.raw <- attachmentMessage("one", InputAttachment{ID: "1", Name: "one.txt", URL: "https://cdn.example/one.txt", Size: 10})
	gateway.raw <- attachmentMessage("two", InputAttachment{ID: "2", Name: "two.txt", URL: "https://cdn.example/two.txt", Size: 11})
	gateway.startIntake(ctx)
	gateway.startIntake(ctx) // must be exactly-once

	first := <-gateway.messages
	second := <-gateway.messages
	if len(first.Stored) != 1 || first.Stored[0].Name != "one.txt" {
		t.Fatalf("first = %+v", first)
	}
	if len(second.Stored) != 1 || second.Stored[0].Name != "two.txt" {
		t.Fatalf("second = %+v", second)
	}
	if first.ChannelID != testChannel || first.MessageID != testMessage || first.AuthorID != testAuthor {
		t.Fatalf("routing identity lost in the pump: %+v", first)
	}
	// Arrival order is preserved and nothing was downloaded twice.
	if urls := stub.seen(); len(urls) != 2 || !strings.HasSuffix(urls[0], "one.txt") || !strings.HasSuffix(urls[1], "two.txt") {
		t.Fatalf("downloads = %v", urls)
	}

	// Close stops the pump instead of leaking it: a message queued afterwards is
	// never hydrated or forwarded.
	if err := gateway.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	gateway.raw <- attachmentMessage("late", InputAttachment{ID: "3", Name: "three.txt", URL: "https://cdn.example/three.txt", Size: 10})
	select {
	case extra := <-gateway.messages:
		t.Fatalf("the intake pump outlived the gateway: %+v", extra)
	case <-time.After(200 * time.Millisecond):
	}
	if urls := stub.seen(); len(urls) != 2 {
		t.Fatalf("a closed gateway must not download again: %v", urls)
	}
}

func TestPumpIntakeSurvivesABrokenFile(t *testing.T) {
	store := newTestStore(t, 1024, 4<<20)
	gateway := gatewayWithREST(t, &multipartRecorder{})
	gateway.ConfigureAttachments(store, stubClient(&stubRoundTripper{status: http.StatusNotFound}), AttachmentFileConfig{DownloadTimeout: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gateway.startIntake(ctx)
	gateway.raw <- attachmentMessage("still important", InputAttachment{ID: "1", Name: "gone.txt", URL: "https://cdn.example/gone.txt", Size: 1 << 30})
	message := <-gateway.messages
	if len(message.Stored) != 0 || len(message.Problems) != 0 && message.Problems[0].Reason != notStoredSize {
		t.Fatalf("stored=%+v problems=%+v", message.Stored, message.Problems)
	}
	if !strings.HasPrefix(message.Content, "still important") {
		t.Fatalf("the turn was damaged by the failed file: %q", message.Content)
	}
}

func TestReceiveStartsTheIntakePump(t *testing.T) {
	gateway := gatewayWithREST(t, &multipartRecorder{})
	store := newTestStore(t, 1<<20, 4<<20)
	gateway.ConfigureAttachments(store, stubClient(&stubRoundTripper{body: textAttachment}), AttachmentFileConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inputs, err := gateway.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gateway.raw <- attachmentMessage("", InputAttachment{ID: "1", Name: "a.txt", URL: "https://cdn.example/a.txt", Size: int64(len(textAttachment))})
	select {
	case input := <-inputs:
		if input.Metadata[MetaAttachmentCount] != "1" {
			t.Fatalf("Receive path did not hydrate: %+v", input.Metadata)
		}
		if strings.TrimSpace(input.Turn.Content[0].Text) == "" {
			t.Fatal("attachment-only message from Receive became an empty turn")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no input reached the harness")
	}
}
