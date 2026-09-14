package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Tulipskun/ai/sdk"
	"github.com/bwmarrin/discordgo"
)

type discordHTTPMock struct {
	mu                    sync.Mutex
	messages              map[string]string
	order                 []string
	sends, edits, deletes int
	failSend, failEdit    int
	channels              []string
}

func (m *discordHTTPMock) RoundTrip(req *http.Request) (*http.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bits := strings.Split(req.URL.Path, "/")
	id := bits[len(bits)-1]
	var body struct {
		Content string                    `json:"content"`
		Embeds  []*discordgo.MessageEmbed `json:"embeds"`
	}
	if req.Body != nil && req.Method != http.MethodDelete {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
	}
	text := body.Content
	if len(body.Embeds) > 0 {
		text = body.Embeds[0].Description
		if discordLength(text) > 4096 {
			return nil, fmt.Errorf("oversized embed: %d", discordLength(text))
		}
	}
	for i, bit := range bits {
		if bit == "channels" && i+1 < len(bits) {
			m.channels = append(m.channels, bits[i+1])
		}
	}
	switch req.Method {
	case http.MethodPost:
		m.sends++
		if m.failSend > 0 {
			m.failSend--
			return nil, errors.New("mock send failure")
		}
		id = fmt.Sprint(m.sends)
		m.order = append(m.order, id)
		m.messages[id] = text
	case http.MethodPatch:
		m.edits++
		if m.failEdit > 0 {
			m.failEdit--
			return nil, errors.New("mock edit failure")
		}
		m.messages[id] = text
	case http.MethodDelete:
		m.deletes++
		delete(m.messages, id)
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"` + id + `"}`)), Request: req}, nil
}
func mockGateway(t *testing.T) (*Gateway, *discordHTTPMock) {
	t.Helper()
	m := &discordHTTPMock{messages: map[string]string{}}
	session, err := discordgo.New("Bot mock")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = &http.Client{Transport: m}
	g := &Gateway{session: session, toolTrace: map[string]*toolTraceState{}, retryStatus: map[string]string{}}
	t.Cleanup(func() {
		g.toolTraceMu.Lock()
		defer g.toolTraceMu.Unlock()
		for _, s := range g.toolTrace {
			if s.flushTimer != nil {
				s.flushTimer.Stop()
			}
			if s.footerTimer != nil {
				s.footerTimer.Stop()
			}
		}
	})
	return g, m
}

// Keep asynchronous throttling timers pending; tests explicitly flush snapshots.
func holdFlush(g *Gateway, channel string) {
	g.toolTraceMu.Lock()
	defer g.toolTraceMu.Unlock()
	g.traceState(channel).lastPushMs = nowMillis() + time.Hour.Milliseconds()
}
func assertPages(t *testing.T, text string, max int) {
	t.Helper()
	pages := paginateDiscord(text, max)
	var raw strings.Builder
	for _, p := range pages {
		if !utf8.ValidString(p.text) || discordLength(p.text) > max {
			t.Fatalf("invalid page (%d/%d units): %q", discordLength(p.text), max, p.text)
		}
		raw.WriteString(p.source)
	}
	if raw.String() != text {
		t.Fatal("pagination changed source content")
	}
}
func TestDiscordPaginationUnicodeWhitespaceAndFences(t *testing.T) {
	for _, text := range []string{
		strings.Repeat("ภาษาไทย 🙂👩‍💻 \n\n\t", 1000),
		"  \n" + strings.Repeat("x", 1899) + " \n\n\tท้าย🙂 ",
		"ก่อน\n```go\n" + strings.Repeat("fmt.Println(\"ไทย🙂\")\n", 1000) + "```\nหลัง\n",
		"~~~python\n" + strings.Repeat("print('🙂')\n", 1000) + "~~~\n",
		"````md\n```\n" + strings.Repeat("x\n", 5000) + "````\n",
	} {
		for _, limit := range []int{1900, maxEmbedChars} {
			assertPages(t, text, limit)
		}
	}
	pages := paginateDiscord("```go\n"+strings.Repeat("ไทย🙂\n", 2000)+"```\n", 1900)
	if len(pages) < 2 {
		t.Fatal("expected multiple code pages")
	}
	for i, p := range pages {
		if !strings.HasPrefix(p.text, "```go\n") {
			t.Fatalf("page %d lacks language fence", i)
		}
		if !strings.HasSuffix(p.text, "```") && !strings.HasSuffix(p.text, "```\n") {
			t.Fatalf("page %d lacks closing fence", i)
		}
	}
}
func TestFinalResponseFallbackPaginatedAndRouted(t *testing.T) {
	g, m := mockGateway(t)
	text := strings.Repeat("ไทย🙂 \n", 900)
	out := sdk.Output{Source: "discord", SessionID: "custom-mapped-session", Metadata: map[string]string{"channel_id": "destination", "secret": "hidden"}, Response: sdk.Response{Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: text}}}}
	if err := g.Display(context.Background(), out); err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	for _, id := range m.order {
		page := m.messages[id]
		if discordLength(page) > 2000 {
			t.Fatal("oversized message")
		}
		got.WriteString(page)
	}
	if got.String() != text {
		t.Fatal("final content altered")
	}
	for _, c := range m.channels {
		if c != "destination" {
			t.Fatalf("misrouted to %s", c)
		}
	}
}
func TestStreamPaginationEditsWithoutTerminalReplay(t *testing.T) {
	g, m := mockGateway(t)
	holdFlush(g, "c")
	display := Display{Sender: g}
	text := "```go\n" + strings.Repeat("println(\"ไทย🙂\")\n", 800) + "```\n \nDone 🙂 "
	runes := []rune(text)
	for start := 0; start < len(runes); start += 617 {
		end := start + 617
		if end > len(runes) {
			end = len(runes)
		}
		displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceResponseContent, Text: string(runes[start:end])})
		if err := g.flushToolTrace(context.Background(), "c"); err != nil {
			t.Fatal(err)
		}
		holdFlush(g, "c")
	}
	before := m.sends
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceResponse, Response: &sdk.Response{Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: text}}}})
	if m.sends != before {
		t.Fatal("terminal response replayed stream")
	}
	pages := paginateDiscord(text, maxEmbedChars)
	state := g.toolTrace["c"]
	if len(state.textPages) != len(pages) {
		t.Fatalf("pages = %d, want %d", len(state.textPages), len(pages))
	}
	for i, p := range pages {
		if m.messages[state.textPages[i].id] != p.text {
			t.Fatalf("page %d differs", i)
		}
	}
	if strings.Join(state.items, "") != text {
		t.Fatal("stream buffer truncated")
	}
}
func TestProgressHidesSecretsAndInternalChatter(t *testing.T) {
	sender := &routingFakeSender{}
	display := Display{Sender: sender}
	secret := "API_KEY=top-secret /private/path delegated task text"
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceResponseText, Text: secret, Message: secret})
	for _, name := range []string{"plan", "delegate_to_subagent", "subagent_history", "subagent_status", "accept_subagent_result", "run_command"} {
		call := &sdk.ToolCall{Name: name, Arguments: secret}
		displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceToolCall, ToolCall: call, Message: secret})
		displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceToolResult, ToolCall: call, ToolResult: &sdk.ToolResult{Content: secret}})
	}
	got := strings.Join(sender.tools, "\n")
	if strings.Contains(got, secret) || strings.Contains(got, "subagent") || strings.Contains(got, "plan") {
		t.Fatalf("leaked progress: %s", got)
	}
	if len(sender.tools) != 2 {
		t.Fatalf("internal chatter: %q", sender.tools)
	}
}
func TestTraceTransitionsRetainBufferOnSendFailure(t *testing.T) {
	for _, fromText := range []bool{false, true} {
		t.Run(fmt.Sprint(fromText), func(t *testing.T) {
			g, m := mockGateway(t)
			holdFlush(g, "c")
			ctx := context.Background()
			if err := g.appendTraceItem(ctx, "c", "original ไทย🙂", fromText); err != nil {
				t.Fatal(err)
			}
			m.failSend = 1
			if err := g.appendTraceItem(ctx, "c", "next", !fromText); err == nil {
				t.Fatal("missing transition send error")
			}
			s := g.toolTrace["c"]
			if !s.dirty || s.isText != fromText || strings.Join(s.items, "") != "original ไทย🙂" {
				t.Fatalf("dropped original state: %+v", s)
			}
			// Retry flushing, not replaying the callback: incoming items are retained.
			if err := g.flushToolTrace(ctx, "c"); err != nil {
				t.Fatal(err)
			}
			if len(m.messages) != 2 || m.messages[m.order[1]] != "next" {
				t.Fatalf("successful messages = %v", m.messages)
			}
		})
	}
}
func TestStreamPageFailureRetriesOnlyUnsentPages(t *testing.T) {
	g, m := mockGateway(t)
	holdFlush(g, "c")
	ctx := context.Background()
	if err := g.appendTextTrace(ctx, "c", strings.Repeat("🙂", 3000)); err != nil {
		t.Fatal(err)
	}
	// Pre-send the first page, then fail sending the next page.
	s := g.toolTrace["c"]
	pages := paginateDiscord(s.items[0], maxEmbedChars)
	id, err := g.SendEmbed(ctx, "c", toolTraceEmbed([]string{pages[0].text}, ""))
	if err != nil {
		t.Fatal(err)
	}
	s.textPages = []tracePageState{{id: id, text: pages[0].text}}
	m.failSend = 1
	if err := g.flushToolTrace(ctx, "c"); err == nil {
		t.Fatal("missing page send error")
	}
	if !s.dirty || s.flushTimer == nil {
		t.Fatal("failed snapshot must remain retryable")
	}
	if err := g.flushToolTrace(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	if len(m.messages) != len(pages) || m.messages[id] != pages[0].text {
		t.Fatal("retry duplicated or dropped sent page")
	}
}
func TestTransitionEditFailurePreservesSnapshotForRetry(t *testing.T) {
	g, m := mockGateway(t)
	ctx := context.Background()
	holdFlush(g, "c")
	if err := g.appendTextTrace(ctx, "c", "first "); err != nil {
		t.Fatal(err)
	}
	if err := g.flushToolTrace(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	holdFlush(g, "c")
	if err := g.appendTextTrace(ctx, "c", "ไทย🙂"); err != nil {
		t.Fatal(err)
	}
	m.failEdit = 1
	if err := g.appendToolTrace(ctx, "c", "Working"); err == nil {
		t.Fatal("missing edit failure")
	}
	if !g.toolTrace["c"].isText {
		t.Fatal("reset text on failed edit")
	}
	if err := g.flushToolTrace(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	if m.sends != 2 || m.messages[m.order[0]] != "first ไทย🙂" {
		t.Fatal("retry did not edit original message")
	}
}

type terminalFailureSender struct {
	routingFakeSender
	cleared int
}

func (f *terminalFailureSender) flushToolTrace(context.Context, string) error {
	f.flushes++
	return errors.New("flush failed")
}
func (f *terminalFailureSender) SendStatusMessage(context.Context, string, string) (string, error) {
	return "retry", nil
}
func (f *terminalFailureSender) EditMessage(context.Context, string, string, string) error {
	return nil
}
func (f *terminalFailureSender) DeleteMessage(context.Context, string, string) error     { return nil }
func (f *terminalFailureSender) updateRetryStatus(context.Context, string, string) error { return nil }
func (f *terminalFailureSender) clearRetryStatus(context.Context, string) error {
	f.cleared++
	return errors.New("cleanup failed")
}
func TestTerminalCleanupAttemptsAllOperationsAndPropagatesErrors(t *testing.T) {
	for _, stage := range []sdk.TraceStage{sdk.TraceResponse, sdk.TraceError} {
		sender := &terminalFailureSender{}
		display := Display{Sender: sender}
		err := display.displayTrace(context.Background(), "c", sdk.TraceEvent{Stage: stage, Err: errors.New("secret-body")})
		if err == nil || !strings.Contains(err.Error(), "flush failed") || !strings.Contains(err.Error(), "cleanup failed") {
			t.Fatalf("errors = %v", err)
		}
		if sender.footerStops != 1 || sender.flushes != 1 || sender.cleared != 1 {
			t.Fatalf("cleanup not attempted: %+v", sender)
		}
		if strings.Contains(strings.Join(sender.tools, ""), "secret-body") {
			t.Fatal("raw error leaked")
		}
	}
}
func TestErrorTerminalFlushesTextStopsTimersAndClearsRetryStatus(t *testing.T) {
	g, m := mockGateway(t)
	holdFlush(g, "c")
	display := Display{Sender: g}
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceRequest})
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceRetryWait, Err: context.DeadlineExceeded, RetryAfter: 3 * time.Second})
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceResponseContent, Text: "partial ไทย🙂 "})
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceError, Err: context.DeadlineExceeded})
	s := g.toolTrace["c"]
	if s.footerActive || s.footerTimer != nil || s.flushTimer != nil || s.dirty {
		t.Fatalf("terminal state not clean: %+v", s)
	}
	if len(g.retryStatus) != 0 || m.deletes != 1 {
		t.Fatal("retry status not cleared")
	}
	foundText, foundError := false, false
	for _, text := range m.messages {
		foundText = foundText || text == "partial ไทย🙂 "
		foundError = foundError || strings.Contains(text, "request timed out")
	}
	if !foundText || !foundError {
		t.Fatalf("missing text/error: %v", m.messages)
	}
}

type displayHTTPError struct{ code int }

func (e displayHTTPError) Error() string       { return "SECRET provider body, URL and credential" }
func (e displayHTTPError) HTTPStatusCode() int { return e.code }
func TestRetryProgressRetainsSafeStatusAndTiming(t *testing.T) {
	g, m := mockGateway(t)
	display := Display{Sender: g}
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceRetryWait, Err: fmt.Errorf("wrapper: %w", displayHTTPError{429}), RetryAfter: 17 * time.Second, Message: "SECRET delegated instructions"})
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceRetryWait, Err: displayHTTPError{503}, RetryAfter: 5 * time.Second})
	if m.sends != 1 || m.edits != 1 {
		t.Fatalf("retry should update one message: sends=%d edits=%d", m.sends, m.edits)
	}
	text := m.messages[m.order[0]]
	if strings.Contains(text, "SECRET") || !strings.Contains(text, "HTTP 503") || !strings.Contains(text, "5s") {
		t.Fatalf("retry status = %q", text)
	}
	for code, want := range map[int]string{401: "authorization", 403: "authorization", 429: "rate limited", 500: "HTTP 500"} {
		if got := safeErrorSummary(displayHTTPError{code}); !strings.Contains(got, want) || strings.Contains(got, "SECRET") {
			t.Fatalf("safe error = %q", got)
		}
	}
}
func TestErrorTerminalReportsFlushFailureWithoutLosingText(t *testing.T) {
	g, m := mockGateway(t)
	holdFlush(g, "c")
	ctx := context.Background()
	if err := g.appendTextTrace(ctx, "c", "buffered ไทย🙂 "); err != nil {
		t.Fatal(err)
	}
	m.failSend = 1
	err := g.Display(ctx, sdk.Output{Metadata: map[string]string{"channel_id": "c"}, Trace: &sdk.TraceEvent{Stage: sdk.TraceError, Err: context.Canceled}})
	if err == nil {
		t.Fatal("transition error was swallowed")
	}
	foundError, foundText := false, false
	for _, text := range m.messages {
		foundError = foundError || strings.Contains(text, "cancelled")
		foundText = foundText || text == "buffered ไทย🙂 "
	}
	if !foundError || !foundText {
		t.Fatalf("terminal lost error/text: %v", m.messages)
	}
	if g.toolTrace["c"].dirty || g.toolTrace["c"].flushTimer != nil {
		t.Fatal("terminal retry not flushed")
	}
}
func TestResetCancelsTraceAndFooterTimers(t *testing.T) {
	g, _ := mockGateway(t)
	holdFlush(g, "c")
	g.startTurnFooter("c")
	if err := g.appendTextTrace(context.Background(), "c", "pending"); err != nil {
		t.Fatal(err)
	}
	s := g.toolTrace["c"]
	flush, footer := s.flushTimer, s.footerTimer
	if flush == nil || footer == nil {
		t.Fatal("expected timers")
	}
	g.resetToolTrace("c")
	if flush.Stop() || footer.Stop() {
		t.Fatal("reset left timer active")
	}
	if g.toolTrace["c"] != nil {
		t.Fatal("reset retained state")
	}
}
func TestPaginationPathologicalFenceLinesStayWithinLimit(t *testing.T) {
	for _, limit := range []int{16, 32, 100, 1900, 4000} {
		for _, text := range []string{
			"```go\n" + strings.Repeat("`", 10000) + "\n", // oversized closing delimiter
			"```" + strings.Repeat("language", 1000) + "\n" + strings.Repeat("🙂", 3000),
			strings.Repeat("\n```go\nไทย🙂\n```\n", 500),
			"   ~~~\n" + strings.Repeat("ไทย🙂", 2000) + "\n   ~~~\n",
		} {
			assertPages(t, text, limit)
		}
	}
}

func TestPlainStreamWhitespaceIsNotTrimmedOrDuplicated(t *testing.T) {
	g, m := mockGateway(t)
	holdFlush(g, "c")
	display := Display{Sender: g}
	chunks := []string{"hello", " ", "\n\n", "ไทย", "🙂", "\t ", strings.Repeat("🙂 \n", 1800)}
	for _, chunk := range chunks {
		displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceResponseContent, Text: chunk})
	}
	text := strings.Join(chunks, "")
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceResponse, Response: &sdk.Response{Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: text}}}})
	var got strings.Builder
	for _, id := range m.order {
		got.WriteString(m.messages[id])
	}
	if got.String() != text {
		t.Fatal("stream whitespace lost or terminal content duplicated")
	}
}

func TestStreamTransitionFailureRetainsIncomingDeltasWithoutReplay(t *testing.T) {
	g, m := mockGateway(t)
	holdFlush(g, "c")
	display := Display{Sender: g}
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceRequest})
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceProviderReady})
	m.failSend = 2
	// The Harness reports display failures but does not replay callbacks.
	for _, text := range []string{"first ไทย🙂 ", "second\n"} {
		if err := display.displayTrace(context.Background(), "c", sdk.TraceEvent{Stage: sdk.TraceResponseContent, Text: text}); err == nil {
			t.Fatal("missing transition error")
		}
	}
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceResponseContent, Text: "third "})
	displayTraceEvent(t, display, sdk.TraceEvent{Stage: sdk.TraceResponse})
	found := false
	for _, text := range m.messages {
		if text == "first ไทย🙂 second\nthird " {
			found = true
		}
	}
	if !found {
		t.Fatalf("lost incoming deltas: %v", m.messages)
	}
	if len(g.toolTrace["c"].pending) != 0 {
		t.Fatal("terminal pending queue not drained")
	}
}

// Exercise the SDK's real streaming trace order, not just hand-built display
// events: request, provider-ready, tool continuation, deltas, terminal response.
type renderingStreamProvider struct {
	text  string
	calls int
}

func (p *renderingStreamProvider) Name() string                   { return "test" }
func (p *renderingStreamProvider) WithAPIKey(string) sdk.Provider { return p }
func (p *renderingStreamProvider) Generate(context.Context, sdk.Request) (sdk.Response, error) {
	return sdk.Response{}, errors.New("expected streaming request")
}
func (p *renderingStreamProvider) Stream(_ context.Context, _ sdk.Request) (<-chan sdk.Event, error) {
	p.calls++
	runes := []rune(p.text)
	ch := make(chan sdk.Event, len(runes)+2)
	if p.calls == 1 {
		ch <- sdk.Event{Type: sdk.EventToolCall, ToolCall: &sdk.ToolCall{ID: "plan", Name: "plan", Arguments: `{"plan":"private plan step"}`}}
	} else {
		for start := 0; start < len(runes); start += 313 {
			end := start + 313
			if end > len(runes) {
				end = len(runes)
			}
			ch <- sdk.Event{Type: sdk.EventText, Text: string(runes[start:end])}
		}
	}
	ch <- sdk.Event{Type: sdk.EventDone}
	close(ch)
	return ch, nil
}
func TestSDKStreamingTurnRendersLosslesslyThroughToolContinuation(t *testing.T) {
	g, m := mockGateway(t)
	holdFlush(g, "c")
	text := "  ไทย🙂\n```go\n" + strings.Repeat("println(\"🙂\")\n", 1000) + "```\n \tDone "
	p := &renderingStreamProvider{text: text}
	router := sdk.NewRouter()
	keys := sdk.NewKeyPool("mock")
	router.RegisterProvider(sdk.ProviderConfig{ID: "test", Keys: keys, Adapter: sdk.AdapterOpenAI})
	router.Register(sdk.ModelRoute{Provider: "test", Model: "model", Adapter: sdk.AdapterOpenAI})
	client := sdk.NewRouterClient(router)
	client.RegisterAdapter(sdk.AdapterOpenAI, p)
	session := sdk.NewSession(sdk.SessionConfig{ID: "mapped", Provider: "test", Model: "model"}, keys)
	agent := &sdk.Agent{Client: client}
	_, err := agent.RunTurnWithTrace(context.Background(), session, sdk.Turn{Role: sdk.RoleUser}, sdk.Request{Stream: true}, func(ctx context.Context, event sdk.TraceEvent) {
		if err := g.Display(ctx, sdk.Output{Source: "discord", SessionID: "mapped", Metadata: map[string]string{"channel_id": "c"}, Trace: &event}); err != nil {
			t.Error(err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	g.toolTraceMu.Lock()
	defer g.toolTraceMu.Unlock()
	state := g.toolTrace["c"]
	if strings.Join(state.items, "") != text {
		t.Fatal("SDK trace stream lost text")
	}
	pages := paginateDiscord(text, maxEmbedChars)
	if len(state.textPages) != len(pages) {
		t.Fatal("terminal replay or page loss")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, page := range pages {
		if m.messages[state.textPages[i].id] != page.text {
			t.Fatalf("page %d differs", i)
		}
	}
	for _, text := range m.messages {
		if strings.Contains(text, "private plan") {
			t.Fatal("internal plan leaked")
		}
	}
	if state.footerActive || state.footerTimer != nil || state.flushTimer != nil {
		t.Fatal("terminal timers still active")
	}
}
