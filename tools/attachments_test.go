package tools

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Tulipskun/ai-engine/runtime/filestore"
	"github.com/Tulipskun/ai-engine/sdk"
)

// The concrete store must keep matching the tool-side interface, so a signature
// drift in runtime/filestore fails here instead of at wiring time.
var _ AttachmentStore = (*filestore.Store)(nil)

const (
	attachmentSessionA = "discord:111111111111111111"
	attachmentSessionB = "discord:222222222222222222"
)

// attachmentFixture is a real file store under a temp directory plus a registry
// that has it injected. Nothing here touches the network or a live transport.
type attachmentFixture struct {
	store    *filestore.Store
	registry *Registry
}

func newAttachmentFixture(t *testing.T) *attachmentFixture {
	t.Helper()
	stateRoot := t.TempDir()
	store, err := filestore.OpenUnder(stateRoot, "data/attachments", filestore.Limits{
		MaxFileBytes:    8 << 20,
		MaxSessionBytes: 24 << 20,
		TTL:             time.Hour,
	})
	if err != nil {
		t.Fatalf("open attachment store: %v", err)
	}
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry.SetAttachmentStore(store)
	return &attachmentFixture{store: store, registry: registry}
}

func (f *attachmentFixture) put(t *testing.T, sessionKey, name string, body []byte) filestore.Ref {
	t.Helper()
	ref, err := f.store.Put(context.Background(), sessionKey, name, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Put(%s): %v", name, err)
	}
	return ref
}

func (f *attachmentFixture) putTyped(t *testing.T, sessionKey, name, contentType string, body []byte) filestore.Ref {
	t.Helper()
	ref, err := f.store.PutWithContentType(context.Background(), sessionKey, name, contentType, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("PutWithContentType(%s): %v", name, err)
	}
	return ref
}

func attachmentContext(sessionID string) context.Context {
	if sessionID == "" {
		return context.Background()
	}
	return sdk.WithSessionID(context.Background(), sessionID)
}

func executeAttachment(t *testing.T, r *Registry, ctx context.Context, name string, args any) (string, bool) {
	t.Helper()
	result := r.Execute(ctx, sdkCall(name, args))
	return result.Content, result.IsError
}

func TestListAttachmentsReturnsReferencesOnly(t *testing.T) {
	fixture := newAttachmentFixture(t)
	secret := "TOP-SECRET-DOC-CONTENT"
	textRef := fixture.put(t, attachmentSessionA, "notes.txt", []byte(secret))
	binaryRef := fixture.put(t, attachmentSessionA, "pixel.png", testPNG(t))

	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "list_attachments", map[string]any{})
	if isErr {
		t.Fatalf("list_attachments failed: %s", content)
	}
	if strings.Contains(content, secret) {
		t.Fatalf("attachment content leaked into the listing: %s", content)
	}
	if strings.Contains(content, fixture.store.Root()) {
		t.Fatalf("absolute store path leaked: %s", content)
	}
	var result listAttachmentsResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	if !result.SessionScoped || result.Count != 2 || len(result.Attachments) != 2 {
		t.Fatalf("unexpected listing: %s", content)
	}
	if result.TotalBytes != textRef.Size+binaryRef.Size {
		t.Fatalf("total_bytes=%d want %d", result.TotalBytes, textRef.Size+binaryRef.Size)
	}
	want := map[string]filestore.Ref{textRef.ID: textRef, binaryRef.ID: binaryRef}
	for _, file := range result.Attachments {
		ref, ok := want[file.ID]
		if !ok {
			t.Fatalf("unexpected attachment id %q", file.ID)
		}
		if file.Name != ref.Name || file.ContentType != ref.ContentType || file.Size != ref.Size || file.Path != ref.Path {
			t.Fatalf("reference mismatch: %+v vs %+v", file, ref)
		}
		if !strings.HasPrefix(file.Path, attachmentRefIDForSession(attachmentSessionA)+"/files/") {
			t.Fatalf("relative path is not inside the session directory: %q", file.Path)
		}
		if file.CreatedAt == "" {
			t.Fatalf("created_at missing for %q", file.Name)
		}
	}
	// Oldest first, matching the store's own ordering contract.
	if result.Attachments[0].Name != "notes.txt" {
		t.Fatalf("attachments are not oldest first: %+v", result.Attachments)
	}
}

func TestListAttachmentsIsSessionScoped(t *testing.T) {
	fixture := newAttachmentFixture(t)
	fixture.put(t, attachmentSessionA, "private.txt", []byte("session a only"))

	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionB), "list_attachments", map[string]any{})
	if isErr {
		t.Fatalf("empty listing must not error: %s", content)
	}
	var result listAttachmentsResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	if result.Count != 0 || len(result.Attachments) != 0 {
		t.Fatalf("session B saw session A files: %s", content)
	}
	if !strings.Contains(content, `"attachments":[]`) {
		t.Fatalf("empty listing must still be an array: %s", content)
	}
}

func TestReadAttachmentText(t *testing.T) {
	fixture := newAttachmentFixture(t)
	body := "line one\nline two\nascii only\n"
	ref := fixture.put(t, attachmentSessionA, "plain.txt", []byte(body))

	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "read_attachment", map[string]any{"ref_id": ref.ID})
	if isErr {
		t.Fatalf("read_attachment failed: %s", content)
	}
	var result readAttachmentResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	if result.Text != body {
		t.Fatalf("text=%q want %q", result.Text, body)
	}
	if result.Truncated || result.Chars != len([]rune(body)) || result.BytesRead != int64(len(body)) {
		t.Fatalf("unexpected result flags: %+v", result)
	}
	if result.Lines != 3 {
		t.Fatalf("lines=%d want 3", result.Lines)
	}
	if result.Kind != "text" || result.Charset != "utf-8" {
		t.Fatalf("unexpected classification: %+v", result)
	}
	if result.File.ID != ref.ID || result.File.Name != "plain.txt" || result.File.Path != ref.Path {
		t.Fatalf("reference not carried through: %+v", result.File)
	}
	if !strings.Contains(content, `"content_type":"text/plain; charset=utf-8"`) {
		t.Fatalf("content type missing: %s", content)
	}
	// JSON escaping keeps the result a single canonical text value.
	if !json.Valid([]byte(content)) {
		t.Fatalf("result is not valid JSON: %s", content)
	}
}

func TestReadAttachmentReadsUTF8Text(t *testing.T) {
	fixture := newAttachmentFixture(t)
	body := "สวัสดี emoji 🙂\nsecond line\n"
	ref := fixture.put(t, attachmentSessionA, "thai.txt", []byte(body))
	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "read_attachment", map[string]any{"ref_id": ref.ID})
	if isErr {
		t.Fatalf("read_attachment failed: %s", content)
	}
	var result readAttachmentResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	if result.Text != body {
		t.Fatalf("text=%q want %q", result.Text, body)
	}
	if result.Chars != len([]rune(body)) {
		t.Fatalf("chars=%d want %d", result.Chars, len([]rune(body)))
	}
}

func TestReadAttachmentHonoursMaxBytesAndTruncates(t *testing.T) {
	fixture := newAttachmentFixture(t)
	body := strings.Repeat("abcdefgh", 400)
	ref := fixture.put(t, attachmentSessionA, "long.txt", []byte(body))

	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "read_attachment", map[string]any{"ref_id": ref.ID, "max_bytes": 64})
	if isErr {
		t.Fatalf("read_attachment failed: %s", content)
	}
	var result readAttachmentResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Truncated {
		t.Fatalf("expected truncated flag: %s", content)
	}
	if result.BytesRead != 64 || len(result.Text) != 64 {
		t.Fatalf("max_bytes not applied: bytes=%d chars=%d", result.BytesRead, len(result.Text))
	}
	if result.File.Size != int64(len(body)) {
		t.Fatalf("declared size changed: %d", result.File.Size)
	}
}

func TestReadAttachmentClampsOversizedMaxBytes(t *testing.T) {
	fixture := newAttachmentFixture(t)
	body := bytes.Repeat([]byte("x"), int(maxAttachmentReadBytes)+128)
	ref := fixture.put(t, attachmentSessionA, "big.txt", body)

	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "read_attachment", map[string]any{"ref_id": ref.ID, "max_bytes": 1 << 40})
	if isErr {
		t.Fatalf("read_attachment failed: %s", content)
	}
	var result readAttachmentResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Truncated {
		t.Fatalf("expected truncation past the tool cap: %s", content)
	}
	if result.BytesRead > maxAttachmentReadBytes {
		t.Fatalf("bytes_read=%d exceeds tool cap %d", result.BytesRead, maxAttachmentReadBytes)
	}
	// The character cap is what reaches the model, and it is well below the byte cap.
	if result.Chars > maxAttachmentReadChars {
		t.Fatalf("chars=%d exceeds text cap %d", result.Chars, maxAttachmentReadChars)
	}
	if len(content) > maxAttachmentReadChars+4096 {
		t.Fatalf("tool result is unbounded: %d bytes", len(content))
	}
}

func TestReadAttachmentRejectsBinary(t *testing.T) {
	fixture := newAttachmentFixture(t)
	ref := fixture.put(t, attachmentSessionA, "pixel.png", testPNG(t))

	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "read_attachment", map[string]any{"ref_id": ref.ID})
	if !isErr {
		t.Fatalf("expected a binary refusal, got: %s", content)
	}
	for _, want := range []string{"image/png", "describe_attachment", "not readable text", strconv.Itoa(int(ref.Size))} {
		if !strings.Contains(content, want) {
			t.Fatalf("refusal %q missing %q", content, want)
		}
	}
	if strings.Contains(content, base64.StdEncoding.EncodeToString(testPNG(t))) {
		t.Fatalf("binary payload leaked into the error text: %s", content)
	}
}

func TestReadAttachmentRejectsUndeclaredBinary(t *testing.T) {
	fixture := newAttachmentFixture(t)
	body := append([]byte{0x00, 0x01, 0x02, 0xff, 0xfe, 0x00}, bytes.Repeat([]byte{0x00}, 32)...)
	ref := fixture.putTyped(t, attachmentSessionA, "mystery.bin", "text/plain; charset=utf-8", body)
	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "read_attachment", map[string]any{"ref_id": ref.ID})
	if !isErr {
		t.Fatalf("expected a refusal for bytes that are not UTF-8: %s", content)
	}
	if !utf8.ValidString(content) {
		t.Fatalf("raw bytes leaked into a tool result: %q", content)
	}
	if strings.ContainsRune(content, 0) || strings.Contains(content, "\xff\xfe") {
		t.Fatalf("non-text bytes leaked into the refusal: %q", content)
	}
	if !strings.Contains(content, "describe_attachment") {
		t.Fatalf("refusal must point at describe_attachment: %s", content)
	}
}

func TestReadAttachmentRejectsMislabelledImage(t *testing.T) {
	fixture := newAttachmentFixture(t)
	// The transport recorded a text type, but the bytes are a PNG. The magic
	// bytes must win so no binary payload can reach the text path by lying about
	// its content type.
	ref := fixture.putTyped(t, attachmentSessionA, "sneaky.txt", "text/plain; charset=utf-8", testPNG(t))
	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "read_attachment", map[string]any{"ref_id": ref.ID})
	if !isErr {
		t.Fatalf("mislabelled PNG was read as text: %s", content)
	}
	if !strings.Contains(content, "describe_attachment") {
		t.Fatalf("refusal must point at describe_attachment: %s", content)
	}
	described, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "describe_attachment", map[string]any{"ref_id": ref.ID})
	if isErr {
		t.Fatalf("describe_attachment failed: %s", described)
	}
	var result describeAttachmentResult
	if err := json.Unmarshal([]byte(described), &result); err != nil {
		t.Fatal(err)
	}
	if result.Kind != "image" || result.Format != "png" || result.Width != 4 {
		t.Fatalf("magic bytes did not classify the file: %s", described)
	}
}

func TestReadAttachmentRefusesPDFAsText(t *testing.T) {
	fixture := newAttachmentFixture(t)
	ref := fixture.put(t, attachmentSessionA, "doc.pdf", testPDF(t))
	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "read_attachment", map[string]any{"ref_id": ref.ID})
	if !isErr {
		t.Fatalf("expected a PDF refusal, got: %s", content)
	}
	if !strings.Contains(content, "application/pdf") || !strings.Contains(content, "describe_attachment") {
		t.Fatalf("unexpected refusal: %s", content)
	}
}

func TestReadAttachmentEmptyFile(t *testing.T) {
	fixture := newAttachmentFixture(t)
	ref := fixture.putTyped(t, attachmentSessionA, "empty.txt", "text/plain; charset=utf-8", nil)
	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "read_attachment", map[string]any{"ref_id": ref.ID})
	if isErr {
		t.Fatalf("empty attachment must read as empty text: %s", content)
	}
	var result readAttachmentResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	if result.Text != "" || result.Chars != 0 || result.Lines != 0 || result.Truncated {
		t.Fatalf("unexpected empty result: %s", content)
	}
}

func TestReadAttachmentTruncationDoesNotBreakMultiByteText(t *testing.T) {
	fixture := newAttachmentFixture(t)
	// "ก" is 3 bytes; cutting the byte budget in the middle of the last rune
	// must still return the text, not an "invalid UTF-8" error.
	body := "กกก tail"
	ref := fixture.put(t, attachmentSessionA, "thai.txt", []byte(body))
	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "read_attachment", map[string]any{"ref_id": ref.ID, "max_bytes": len(body) - 1})
	if isErr {
		t.Fatalf("byte truncation must not fail a valid UTF-8 read: %s", content)
	}
	var result readAttachmentResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Truncated {
		t.Fatalf("expected the truncated flag: %s", content)
	}
	if !utf8.ValidString(result.Text) {
		t.Fatalf("truncated text is not valid UTF-8: %q", result.Text)
	}
	if !strings.HasPrefix(result.Text, "กก") {
		t.Fatalf("unexpected text: %q", result.Text)
	}
}

func TestDescribeAttachmentImages(t *testing.T) {
	fixture := newAttachmentFixture(t)
	for name, body := range map[string][]byte{"pixel.png": testPNG(t), "pixel.jpg": testJPEG(t), "pixel.gif": testGIF(t)} {
		ref := fixture.put(t, attachmentSessionA, name, body)
		content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "describe_attachment", map[string]any{"ref_id": ref.ID})
		if isErr {
			t.Fatalf("describe_attachment(%s) failed: %s", name, content)
		}
		var result describeAttachmentResult
		if err := json.Unmarshal([]byte(content), &result); err != nil {
			t.Fatal(err)
		}
		if result.Kind != "image" {
			t.Fatalf("%s kind=%q", name, result.Kind)
		}
		if result.Width != 4 || result.Height != 3 {
			t.Fatalf("%s dimensions=%dx%d want 4x3", name, result.Width, result.Height)
		}
		if result.File.Size != int64(len(body)) || result.File.Path != ref.Path {
			t.Fatalf("%s metadata not carried through: %+v", name, result)
		}
		if result.BytesProcessed > int64(len(body)) {
			t.Fatalf("%s processed more bytes than stored", name)
		}
	}
}

func TestDescribeAttachmentReportsUndecodableImage(t *testing.T) {
	fixture := newAttachmentFixture(t)
	body := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), bytes.Repeat([]byte{0x01}, 64)...)
	ref := fixture.putTyped(t, attachmentSessionA, "photo.webp", "image/webp", body)
	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "describe_attachment", map[string]any{"ref_id": ref.ID})
	if isErr {
		t.Fatalf("describe_attachment must degrade to a note: %s", content)
	}
	var result describeAttachmentResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	if result.Kind != "image" || result.Width != 0 || result.Height != 0 {
		t.Fatalf("unexpected result: %s", content)
	}
	joined := strings.Join(result.Notes, " ")
	if !strings.Contains(joined, "dimensions") || !strings.Contains(joined, "image/webp") {
		t.Fatalf("notes must disclose the limitation: %s", joined)
	}
}

func TestDescribeAttachmentPDFHandlesMinifiedContentStream(t *testing.T) {
	fixture := newAttachmentFixture(t)
	// A content stream with no separators at all: a token walker still has to
	// reach the strings, while a line-based reader would not.
	minified := pdfContentStream(t, "BT/F1 8 Tf(CompactOne)Tj(CompactTwo)Tj[(ArrOne)-30(ArrTwo)]TJ ET")
	body := append([]byte("%PDF-1.1\n1 0 obj\n<< /Length "), []byte(strconv.Itoa(len(minified)))...)
	body = append(body, []byte(" /Filter /FlateDecode >>\nstream\n")...)
	body = append(body, minified...)
	body = append(body, []byte("\nendstream\nendobj\ntrailer << /Root 1 0 R >>\n%%EOF\n")...)
	ref := fixture.putTyped(t, attachmentSessionA, "min.pdf", "application/pdf", body)
	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "describe_attachment", map[string]any{"ref_id": ref.ID})
	if isErr {
		t.Fatalf("describe_attachment failed: %s", content)
	}
	var result describeAttachmentResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, "CompactOne") || !strings.Contains(result.Text, "CompactTwo") || !strings.Contains(result.Text, "ArrOneArrTwo") {
		t.Fatalf("minified content stream not parsed: %q", result.Text)
	}
	if result.TextOperators < 2 {
		t.Fatalf("text_operators=%d", result.TextOperators)
	}
}

func TestDescribeAttachmentPDF(t *testing.T) {
	fixture := newAttachmentFixture(t)
	body := testPDF(t)
	ref := fixture.put(t, attachmentSessionA, "report.pdf", body)

	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "describe_attachment", map[string]any{"ref_id": ref.ID})
	if isErr {
		t.Fatalf("describe_attachment failed: %s", content)
	}
	var result describeAttachmentResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	if result.Kind != "pdf" || result.Format != "pdf" {
		t.Fatalf("kind=%q format=%q", result.Kind, result.Format)
	}
	if result.PageCount != 2 || !result.PagesDiscovered {
		t.Fatalf("page_count=%d discovered=%v", result.PageCount, result.PagesDiscovered)
	}
	if result.Images != 1 {
		t.Fatalf("embedded_images=%d want 1", result.Images)
	}
	if !result.ExtractedFromPDF || !strings.Contains(result.Text, "Quarterly summary") || !strings.Contains(result.Text, "second line of the report") {
		t.Fatalf("extracted text wrong: %q", result.Text)
	}
	if !strings.Contains(result.Text, "escaped (paren)") {
		t.Fatalf("PDF escape sequences not decoded: %q", result.Text)
	}
	if !strings.Contains(result.Text, "AB") {
		t.Fatalf("hex string operand not decoded: %q", result.Text)
	}
	if !strings.Contains(result.Text, "minified TJ array") {
		t.Fatalf("minified TJ array operand not decoded: %q", result.Text)
	}
	if result.Chars != len([]rune(result.Text)) || result.Chars == 0 {
		t.Fatalf("char count mismatch: %+v", result)
	}
	if result.TextOperators < 3 {
		t.Fatalf("text_operators=%d", result.TextOperators)
	}
	if result.Truncated {
		t.Fatalf("unexpected truncation: %s", content)
	}
	joined := strings.Join(result.Notes, " ")
	if !strings.Contains(joined, "DCTDecode") {
		t.Fatalf("undecodable stream must be disclosed: %s", joined)
	}
	// Raw compressed payloads must never reach the model.
	if strings.Contains(content, base64.StdEncoding.EncodeToString(body)) {
		t.Fatalf("PDF bytes leaked into the description: %s", content)
	}
}

func TestDescribeAttachmentPDFReportsUnknownPageCount(t *testing.T) {
	fixture := newAttachmentFixture(t)
	// A PDF whose only structure is an object stream: page objects are not
	// visible, and the summariser must say so instead of inventing a count.
	body := []byte("%PDF-1.5\n1 0 obj\n<< /Type /ObjStm /N 1 /First 4 >>\nstream\n1 0\nendstream\nendobj\ntrailer << /Root 1 0 R >>\n%%EOF\n")
	ref := fixture.putTyped(t, attachmentSessionA, "opaque.pdf", "application/pdf", body)
	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "describe_attachment", map[string]any{"ref_id": ref.ID})
	if isErr {
		t.Fatalf("describe_attachment failed: %s", content)
	}
	var result describeAttachmentResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	if result.PagesDiscovered || result.PageCount != 0 {
		t.Fatalf("invented a page count: %s", content)
	}
	joined := strings.Join(result.Notes, " ")
	if !strings.Contains(joined, "page count is unknown") {
		t.Fatalf("missing honest page-count note: %s", joined)
	}
	if !strings.Contains(joined, "Tj/TJ") {
		t.Fatalf("missing text-extraction limitation note: %s", joined)
	}
}

func TestDescribeAttachmentTextTruncates(t *testing.T) {
	fixture := newAttachmentFixture(t)
	body := strings.Repeat("ไทย abc\n", maxAttachmentTextChars)
	ref := fixture.put(t, attachmentSessionA, "long.txt", []byte(body))
	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "describe_attachment", map[string]any{"ref_id": ref.ID})
	if isErr {
		t.Fatalf("describe_attachment failed: %s", content)
	}
	var result describeAttachmentResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Truncated {
		t.Fatalf("expected truncation: %s", content)
	}
	if result.Chars > maxAttachmentTextChars {
		t.Fatalf("chars=%d exceeds cap %d", result.Chars, maxAttachmentTextChars)
	}
	if result.Kind != "text" || result.Charset != "utf-8" || result.Lines == 0 {
		t.Fatalf("unexpected text summary: %+v", result)
	}
	if utf8Len := len([]rune(result.Text)); utf8Len != result.Chars {
		t.Fatalf("chars=%d does not match the text it reports (%d runes)", result.Chars, utf8Len)
	}
}

func TestDescribeAttachmentBinaryMetadataOnly(t *testing.T) {
	fixture := newAttachmentFixture(t)
	var buffer bytes.Buffer
	gwriter := gzip.NewWriter(&buffer)
	if _, err := gwriter.Write(bytes.Repeat([]byte("compress me "), 64)); err != nil {
		t.Fatal(err)
	}
	if err := gwriter.Close(); err != nil {
		t.Fatal(err)
	}
	ref := fixture.putTyped(t, attachmentSessionA, "bundle.gz", "application/gzip", buffer.Bytes())
	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "describe_attachment", map[string]any{"ref_id": ref.ID})
	if isErr {
		t.Fatalf("describe_attachment failed: %s", content)
	}
	var result describeAttachmentResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	if result.Kind != "archive" || result.Format != "gzip" {
		t.Fatalf("unexpected classification: %s", content)
	}
	if result.Text != "" {
		t.Fatalf("binary content must not be summarised as text: %q", result.Text)
	}
	if len(result.Notes) == 0 {
		t.Fatalf("expected a metadata-only note")
	}
}

func TestAttachmentUnknownRefFails(t *testing.T) {
	fixture := newAttachmentFixture(t)
	ctx := attachmentContext(attachmentSessionA)
	for _, name := range []string{"read_attachment", "describe_attachment"} {
		content, isErr := executeAttachment(t, fixture.registry, ctx, name, map[string]any{"ref_id": encodeTestFileID(t, 7)})
		if !isErr {
			t.Fatalf("%s accepted an unknown reference: %s", name, content)
		}
		if !strings.Contains(content, "not found") {
			t.Fatalf("%s error must say not found: %s", name, content)
		}
	}
}

func TestAttachmentInvalidAndTraversalRefsRejected(t *testing.T) {
	fixture := newAttachmentFixture(t)
	ctx := attachmentContext(attachmentSessionA)
	markdown := fixture.put(t, attachmentSessionA, "doc.md", []byte("# heading"))
	target := filepath.Join(fixture.store.Root(), markdown.Path)
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("fixture path moved: %v", err)
	}
	other := attachmentRefIDForSession(attachmentSessionA)
	for _, refID := range []string{
		"",
		"   ",
		"../../manifest.json",
		"../" + other + "/manifest.json",
		".." + string(filepath.Separator) + ".." + string(filepath.Separator) + "manifest.json",
		encodeTestFileID(t, 1) + "/../../manifest.json",
		"./files/" + encodeTestFileID(t, 2),
		"not base64 url \x00",
		encodeTestFileID(t, 3) + "extra",
		strings.TrimSuffix(encodeTestFileID(t, 4), "AA"),
		strings.ToUpper(encodeTestFileID(t, 5)),
		"....",
		"/",
	} {
		for _, name := range []string{"read_attachment", "describe_attachment"} {
			content, isErr := executeAttachment(t, fixture.registry, ctx, name, map[string]any{"ref_id": refID})
			if !isErr {
				t.Fatalf("%s accepted reference %q: %s", name, refID, content)
			}
			if strings.Contains(content, "# heading") {
				t.Fatalf("%s disclosed content through reference %q", name, refID)
			}
			if strings.Contains(content, fixture.store.Root()) {
				t.Fatalf("%s disclosed the absolute store root through reference %q", name, refID)
			}
		}
	}
	// The absolute-path form of a real reference is still an invalid id.
	if _, isErr := executeAttachment(t, fixture.registry, ctx, "read_attachment", map[string]any{"ref_id": target}); !isErr {
		t.Fatal("absolute path reference accepted")
	}
}

func TestAttachmentCrossSessionReadRejected(t *testing.T) {
	fixture := newAttachmentFixture(t)
	secret := "only-session-a-can-read-this"
	ref := fixture.put(t, attachmentSessionA, "secret.txt", []byte(secret))

	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionB), "read_attachment", map[string]any{"ref_id": ref.ID})
	if !isErr {
		t.Fatalf("session B read a session A attachment: %s", content)
	}
	if strings.Contains(content, secret) {
		t.Fatalf("content leaked across sessions: %s", content)
	}
	if strings.Contains(content, "secret.txt") {
		t.Fatalf("file name leaked across sessions: %s", content)
	}
	if _, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionB), "describe_attachment", map[string]any{"ref_id": ref.ID}); !isErr {
		t.Fatal("session B described a session A attachment")
	}
	// Session A still reads its own file.
	content, isErr = executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), "read_attachment", map[string]any{"ref_id": ref.ID})
	if isErr || !strings.Contains(content, secret) {
		t.Fatalf("session A lost access: %s", content)
	}
}

func TestAttachmentToolsIgnoreModelSuppliedSessionKey(t *testing.T) {
	fixture := newAttachmentFixture(t)
	secretA := "planner cannot name a session"
	fixture.put(t, attachmentSessionA, "a.txt", []byte(secretA))
	sessionBRef := fixture.put(t, attachmentSessionB, "b.txt", []byte("other session"))

	recorder := &sessionRecordingStore{inner: fixture.store}
	fixture.registry.SetAttachmentStore(recorder)

	// The model names session A in invented arguments while the context session
	// is B, and asks for B's own file. The store must only ever be asked for B,
	// so the argument cannot redirect the lookup.
	payload := `{"ref_id":"` + sessionBRef.ID + `","session_key":"` + attachmentSessionA + `","session_id":"` + attachmentSessionA + `","max_bytes":4096}`
	content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionB), "read_attachment", json.RawMessage(payload))
	if isErr {
		t.Fatalf("read_attachment of own session file failed: %s", content)
	}
	if strings.Contains(content, secretA) {
		t.Fatalf("session A content leaked: %s", content)
	}
	for _, key := range recorder.keys() {
		if key != attachmentSessionB {
			t.Fatalf("store was queried with session key %q", key)
		}
	}

	// The same trick aimed at session A's reference must fail instead of reading.
	crossPayload := `{"ref_id":"` + mustFirstRefID(t, fixture, attachmentSessionA) + `","session_key":"` + attachmentSessionA + `"}`
	if content, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionB), "read_attachment", json.RawMessage(crossPayload)); !isErr || strings.Contains(content, secretA) {
		t.Fatalf("a model-supplied session key redirected the lookup: %s", content)
	}

	// list_attachments ignores invented keys the same way.
	if _, isErr := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionB), "list_attachments", json.RawMessage(`{"session_key":"`+attachmentSessionA+`"}`)); isErr {
		t.Fatalf("list_attachments rejected an ignored extra argument: %s", content)
	}
	for _, key := range recorder.keys() {
		if key != attachmentSessionB {
			t.Fatalf("listing used session key %q", key)
		}
	}
}

func TestAttachmentToolsWithoutStoreFailCleanly(t *testing.T) {
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := attachmentContext(attachmentSessionA)
	for _, call := range []struct {
		name string
		args any
	}{
		{"list_attachments", map[string]any{}},
		{"read_attachment", map[string]any{"ref_id": encodeTestFileID(t, 9)}},
		{"describe_attachment", map[string]any{"ref_id": encodeTestFileID(t, 9)}},
	} {
		content, isErr := executeAttachment(t, registry, ctx, call.name, call.args)
		if !isErr {
			t.Fatalf("%s succeeded without a store: %s", call.name, content)
		}
		if !strings.Contains(content, "attachment store is not configured") {
			t.Fatalf("%s error must name the missing store: %s", call.name, content)
		}
	}
	// A browser-enabled legacy registry behaves the same.
	browserRegistry, err := NewRegistryWithBrowser(t.TempDir(), NewBrowserClient(BrowserClientConfig{}), true, "")
	if err != nil {
		t.Fatal(err)
	}
	if content, isErr := executeAttachment(t, browserRegistry, ctx, "list_attachments", map[string]any{}); !isErr || !strings.Contains(content, "not configured") {
		t.Fatalf("browser registry did not report the missing store: %s", content)
	}
	// Clearing the store returns a wired registry to the same behaviour.
	registry.SetAttachmentStore(nil)
	if content, isErr := executeAttachment(t, registry, ctx, "list_attachments", map[string]any{}); !isErr || !strings.Contains(content, "not configured") {
		t.Fatalf("cleared store did not restore the error: %s", content)
	}
}

func TestAttachmentToolsRequireSessionContext(t *testing.T) {
	fixture := newAttachmentFixture(t)
	ref := fixture.put(t, attachmentSessionA, "notes.txt", []byte("needs a session"))
	for _, call := range []struct {
		name string
		args any
	}{
		{"list_attachments", map[string]any{}},
		{"read_attachment", map[string]any{"ref_id": ref.ID}},
		{"describe_attachment", map[string]any{"ref_id": ref.ID}},
	} {
		content, isErr := executeAttachment(t, fixture.registry, context.Background(), call.name, call.args)
		if !isErr {
			t.Fatalf("%s succeeded without a session: %s", call.name, content)
		}
		if !strings.Contains(content, "session_id is required") {
			t.Fatalf("%s must ask for a session: %s", call.name, content)
		}
	}
}

func TestAttachmentToolSchemas(t *testing.T) {
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defs := map[string]sdk.Tool{}
	for _, def := range registry.Definitions() {
		defs[def.Name] = def
	}
	list, ok := defs["list_attachments"]
	if !ok {
		t.Fatal("list_attachments is not registered")
	}
	listSchema, isMap := list.InputSchema.(map[string]any)
	if !isMap {
		t.Fatalf("list_attachments schema is not an object: %+v", list.InputSchema)
	}
	if properties, ok := listSchema["properties"].(map[string]any); !ok || len(properties) != 0 {
		t.Fatalf("list_attachments must take no arguments: %+v", list.InputSchema)
	}
	if _, hasRequired := listSchema["required"]; hasRequired {
		t.Fatal("list_attachments must not require arguments")
	}
	for _, name := range []string{"read_attachment", "describe_attachment"} {
		tool, ok := defs[name]
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		schema, isMap := tool.InputSchema.(map[string]any)
		if !isMap {
			t.Fatalf("%s schema is not an object: %+v", name, tool.InputSchema)
		}
		required, _ := schema["required"].([]string)
		if len(required) != 1 || required[0] != "ref_id" {
			t.Fatalf("%s must require only ref_id, got %v", name, required)
		}
		properties, _ := schema["properties"].(map[string]any)
		if _, ok := properties["ref_id"]; !ok {
			t.Fatalf("%s must document ref_id", name)
		}
		if _, ok := properties["max_bytes"]; !ok {
			t.Fatalf("%s must document max_bytes", name)
		}
		for _, forbidden := range []string{"session_key", "session_id", "path", "root", "content"} {
			if _, ok := properties[forbidden]; ok {
				t.Fatalf("%s must not accept %s from the model", name, forbidden)
			}
		}
	}
}

func TestAttachmentToolsNeverEmitBase64OrRawBytes(t *testing.T) {
	fixture := newAttachmentFixture(t)
	body := testPDF(t)
	ref := fixture.put(t, attachmentSessionA, "report.pdf", body)
	encoded := base64.StdEncoding.EncodeToString(body)
	partials := []string{encoded[:64], encoded[len(encoded)/2 : len(encoded)/2+64]}
	for _, call := range []struct {
		name string
		args any
	}{
		{"list_attachments", map[string]any{}},
		{"describe_attachment", map[string]any{"ref_id": ref.ID}},
		{"read_attachment", map[string]any{"ref_id": ref.ID}},
	} {
		content, _ := executeAttachment(t, fixture.registry, attachmentContext(attachmentSessionA), call.name, call.args)
		for _, probe := range partials {
			if strings.Contains(content, probe) {
				t.Fatalf("%s leaked base64 of the stored bytes: %s", call.name, probe)
			}
		}
		if strings.Contains(content, "endstream") || strings.Contains(content, "obj\n<<") {
			t.Fatalf("%s leaked a raw PDF structure: %s", call.name, content)
		}
	}
}

func TestAttachmentResultsStayInRegistryErrorPath(t *testing.T) {
	fixture := newAttachmentFixture(t)
	ctx := attachmentContext(attachmentSessionA)
	if result := fixture.registry.Execute(ctx, sdk.ToolCall{ID: "bad-args", Name: "read_attachment", Arguments: `{"ref_id":`}); !result.IsError {
		t.Fatal("malformed JSON must be an error")
	}
	if result := fixture.registry.Execute(ctx, sdk.ToolCall{ID: "empty", Name: "read_attachment", Arguments: ""}); !result.IsError {
		t.Fatal("missing arguments must be an error")
	}
	ref := fixture.put(t, attachmentSessionA, "ok.txt", []byte("fine"))
	if result := fixture.registry.Execute(ctx, sdk.ToolCall{ID: "ok", Name: "read_attachment", Arguments: `{"ref_id":"` + ref.ID + `"}`}); result.IsError || result.ID != "ok" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

// fakeAttachmentStore proves the tools depend only on the small injected
// interface: it never touches the filesystem, so the same behaviour is testable
// with a stub. Every method is recorded, which is how the test shows the
// session key that reaches the store and that Manifest stays metadata-only.
type fakeAttachmentStore struct {
	calls        []string
	listSession  string
	listErr      error
	getErr       error
	manifest     filestore.Manifest
	manifestBody []byte
}

func (s *fakeAttachmentStore) List(ctx context.Context, sessionKey string) ([]filestore.Meta, error) {
	s.calls = append(s.calls, "List:"+sessionKey)
	s.listSession = sessionKey
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.manifest.Files, nil
}

func (s *fakeAttachmentStore) Get(ctx context.Context, sessionKey, refID string) (io.ReadCloser, filestore.Meta, error) {
	s.calls = append(s.calls, "Get:"+sessionKey+":"+refID)
	if s.getErr != nil {
		return nil, filestore.Meta{}, s.getErr
	}
	for _, meta := range s.manifest.Files {
		if meta.ID == refID {
			return io.NopCloser(bytes.NewReader(s.manifestBody)), meta, nil
		}
	}
	return nil, filestore.Meta{}, filestore.ErrNotFound
}

func (s *fakeAttachmentStore) Manifest(ctx context.Context, sessionKey string) (filestore.Manifest, error) {
	s.calls = append(s.calls, "Manifest:"+sessionKey)
	return s.manifest, nil
}

func (s *fakeAttachmentStore) PutWithContentType(ctx context.Context, sessionKey, name, contentType string, r io.Reader) (filestore.Ref, error) {
	s.calls = append(s.calls, "PutWithContentType:"+sessionKey+":"+name)
	return filestore.Ref{}, filestore.ErrNotFound
}

func TestAttachmentToolsWorkAgainstAnInjectedFake(t *testing.T) {
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("fake-backed text")
	fake := &fakeAttachmentStore{
		manifest:     filestore.Manifest{Version: 1, Files: []filestore.Meta{{ID: encodeTestFileID(t, 5), Name: "fake.txt", ContentType: "text/plain; charset=utf-8", Size: int64(len(body)), Path: "session/files/" + encodeTestFileID(t, 5), CreatedAt: time.Unix(1700000000, 0).UTC()}}},
		manifestBody: body,
	}
	registry.SetAttachmentStore(fake)
	ctx := attachmentContext("cli:fake-session")

	content, isErr := executeAttachment(t, registry, ctx, "list_attachments", map[string]any{})
	if isErr {
		t.Fatalf("list_attachments against a fake store failed: %s", content)
	}
	if !strings.Contains(content, `"name":"fake.txt"`) || !strings.Contains(content, `"created_at":"2023-11-14T22:13:20Z"`) {
		t.Fatalf("unexpected listing: %s", content)
	}
	if strings.Contains(content, "fake-backed text") {
		t.Fatalf("listing must not carry content: %s", content)
	}
	if _, isErr := executeAttachment(t, registry, ctx, "read_attachment", map[string]any{"ref_id": encodeTestFileID(t, 5)}); isErr {
		t.Fatalf("read_attachment against a fake store failed: %s", content)
	}
	if _, isErr := executeAttachment(t, registry, ctx, "describe_attachment", map[string]any{"ref_id": encodeTestFileID(t, 5)}); isErr {
		t.Fatalf("describe_attachment against a fake store failed: %s", content)
	}
	for _, call := range fake.calls {
		if !strings.HasSuffix(call, ":cli:fake-session") && !strings.Contains(call, ":cli:fake-session:") {
			t.Fatalf("store received an unexpected session key: %s", call)
		}
	}
}

func TestAttachmentToolsPropagateStoreErrors(t *testing.T) {
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeAttachmentStore{listErr: errors.New("fake store is unavailable")}
	registry.SetAttachmentStore(fake)
	ctx := attachmentContext("cli:fake-session")
	content, isErr := executeAttachment(t, registry, ctx, "list_attachments", map[string]any{})
	if !isErr || !strings.Contains(content, "fake store is unavailable") {
		t.Fatalf("store error was swallowed: %s", content)
	}
	content, isErr = executeAttachment(t, registry, ctx, "read_attachment", map[string]any{"ref_id": encodeTestFileID(t, 6)})
	if !isErr || !strings.Contains(content, "not found") {
		t.Fatalf("unknown fake reference did not fail: %s", content)
	}
	fake.getErr = errors.New("fake store cannot open the file")
	fake.manifest = filestore.Manifest{Version: 1, Files: []filestore.Meta{{ID: encodeTestFileID(t, 5), Name: "fake.txt", ContentType: "text/plain; charset=utf-8"}}}
	if content, isErr = executeAttachment(t, registry, ctx, "describe_attachment", map[string]any{"ref_id": encodeTestFileID(t, 5)}); !isErr || !strings.Contains(content, "fake store cannot open the file") {
		t.Fatalf("Get error was swallowed: %s", content)
	}
}

// sessionRecordingStore wraps the real store and records the session key of
// every call, which proves the tools derive the key from the context only.
type sessionRecordingStore struct {
	inner AttachmentStore
	mu    sync.Mutex
	seen  []string
}

func (s *sessionRecordingStore) record(sessionKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, sessionKey)
}

func (s *sessionRecordingStore) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

func (s *sessionRecordingStore) List(ctx context.Context, sessionKey string) ([]filestore.Meta, error) {
	s.record(sessionKey)
	return s.inner.List(ctx, sessionKey)
}

func (s *sessionRecordingStore) Get(ctx context.Context, sessionKey, refID string) (io.ReadCloser, filestore.Meta, error) {
	s.record(sessionKey)
	return s.inner.Get(ctx, sessionKey, refID)
}

func (s *sessionRecordingStore) Manifest(ctx context.Context, sessionKey string) (filestore.Manifest, error) {
	s.record(sessionKey)
	return s.inner.Manifest(ctx, sessionKey)
}

func (s *sessionRecordingStore) PutWithContentType(ctx context.Context, sessionKey, name, contentType string, r io.Reader) (filestore.Ref, error) {
	s.record(sessionKey)
	return s.inner.PutWithContentType(ctx, sessionKey, name, contentType, r)
}

// mustFirstRefID returns one stored attachment id for a session.
func mustFirstRefID(t *testing.T, fixture *attachmentFixture, sessionKey string) string {
	t.Helper()
	metas, err := fixture.store.List(context.Background(), sessionKey)
	if err != nil || len(metas) == 0 {
		t.Fatalf("fixture has no attachment for %q: %v", sessionKey, err)
	}
	return metas[0].ID
}

// encodeTestFileID builds a syntactically valid, unowned attachment id.
func encodeTestFileID(t *testing.T, seed byte) string {
	t.Helper()
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{seed}, 16))
}

// attachmentRefIDForSession mirrors filestore.encodeSessionKey, which is the
// directory name a session's attachments live under.
func attachmentRefIDForSession(sessionKey string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(sessionKey))
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	return encodeTestImage(t, func(w io.Writer) error { return png.Encode(w, testGradientImage()) })
}

func testJPEG(t *testing.T) []byte {
	t.Helper()
	return encodeTestImage(t, func(w io.Writer) error { return jpeg.Encode(w, testGradientImage(), &jpeg.Options{Quality: 80}) })
}

func testGIF(t *testing.T) []byte {
	t.Helper()
	return encodeTestImage(t, func(w io.Writer) error { return gif.Encode(w, testGradientImage(), nil) })
}

func encodeTestImage(t *testing.T, encode func(io.Writer) error) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := encode(&buffer); err != nil {
		t.Fatal(err)
	}
	if buffer.Len() == 0 {
		t.Fatal("test image is empty")
	}
	return buffer.Bytes()
}

func testGradientImage() image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 60), G: uint8(y * 80), B: 0x40, A: 0xff})
		}
	}
	return img
}

// testPDF builds a two-page PDF with FlateDecode content streams, one image
// XObject, and one DCTDecode stream that the summariser cannot decode.
func testPDF(t *testing.T) []byte {
	t.Helper()
	first := pdfContentStream(t, "BT /F1 12 Tf (Quarterly summary) Tj T* (second line of the report) Tj ET")
	second := pdfContentStream(t, "BT /F1 10 Tf (Appendix: ) Tj <00410042> Tj [(minified)( )-25(TJ array)]TJ (escaped \\(paren\\)) Tj ET")
	var buffer bytes.Buffer
	write := func(format string, args ...any) {
		if _, err := fmt.Fprintf(&buffer, format, args...); err != nil {
			t.Fatal(err)
		}
	}
	offsets := map[int]int{}
	write("%%PDF-1.4\n")
	offsets[1] = buffer.Len()
	write("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offsets[2] = buffer.Len()
	write("2 0 obj\n<< /Type /Pages /Kids [3 0 R 5 0 R] /Count 2 >>\nendobj\n")
	offsets[3] = buffer.Len()
	write("3 0 obj\n<< /Type /Page /Parent 2 0 R /Contents 4 0 R /Resources << /XObject << /Im0 7 0 R >> >> >>\nendobj\n")
	offsets[4] = buffer.Len()
	write("4 0 obj\n<< /Length %d /Filter /FlateDecode >>\nstream\n", len(first))
	buffer.Write(first)
	write("\nendstream\nendobj\n")
	offsets[5] = buffer.Len()
	write("5 0 obj\n<< /Type /Page /Parent 2 0 R /Contents 6 0 R >>\nendobj\n")
	offsets[6] = buffer.Len()
	write("6 0 obj\n<< /Length %d /Filter /FlateDecode >>\nstream\n", len(second))
	buffer.Write(second)
	write("\nendstream\nendobj\n")
	offsets[7] = buffer.Len()
	write("7 0 obj\n<< /Type /XObject /Subtype /Image /Width 2 /Height 2 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length 6 >>\nstream\nJPEGDATA\nendstream\nendobj\n")
	write("xref\n0 8\n0000000000 65535 f \n")
	for index := 1; index <= 7; index++ {
		write("%010d 00000 n \n", offsets[index])
	}
	write("trailer\n<< /Size 8 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", buffer.Len())
	return buffer.Bytes()
}

func pdfContentStream(t *testing.T, content string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zlib.NewWriter(&buffer)
	if _, err := writer.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestAllToolSchemasMarshalWithoutNulls(t *testing.T) {
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range registry.Definitions() {
		data, err := json.Marshal(def.InputSchema)
		if err != nil {
			t.Fatalf("%s schema does not marshal: %v", def.Name, err)
		}
		var schema map[string]any
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatalf("%s schema is not an object: %s", def.Name, data)
		}
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s schema properties must marshal to an object, got: %s", def.Name, data)
		}
		_ = props
	}
}
