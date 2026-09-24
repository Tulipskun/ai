package mobile

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Tulipskun/ai/sdk"
	"github.com/gorilla/websocket"
)

// Frame kinds on the wire. Additive-only: unknown fields are ignored by both
// sides so an older phone keeps working against a newer daemon.
const (
	FrameHello   = "hello"
	FrameMessage = "message"
	FrameAck     = "ack"
	FrameTrace   = "trace"
	FrameDone    = "done"
	FrameError   = "error"
)

// SourceName is the routing label the harness uses. sdk.DispatchDisplay filters
// displays by this, so the mobile client only receives its own output.
const SourceName = "mobile"

type ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type Inbound struct {
	Type        string        `json:"type"`
	Source      string        `json:"source"`
	SessionID   string        `json:"session_id"`
	Role        string        `json:"role"`
	Content     []ContentPart `json:"content"`
	ClientMsgID string        `json:"client_msg_id"`
}

type ToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// Outbound is what the phone renders. Text, agent attribution and job id let
// the app show main vs sub agent activity both live and after a restart.
type Outbound struct {
	Kind         string        `json:"kind"`
	SessionID    string        `json:"session_id"`
	Role         string        `json:"role"`
	Agent        string        `json:"agent,omitempty"`
	JobID        string        `json:"job_id,omitempty"`
	Stage        string        `json:"stage,omitempty"`
	Seq          int64         `json:"seq,omitempty"`
	Text         string        `json:"text,omitempty"`
	Content      []ContentPart `json:"content,omitempty"`
	ToolCall     *ToolCall     `json:"tool_call,omitempty"`
	ClientMsgID  string        `json:"client_msg_id,omitempty"`
	InputTokens  int           `json:"input_tokens,omitempty"`
	OutputTokens int           `json:"output_tokens,omitempty"`
}

// TokenStore is the daemon's volatile credential (d1store.MemoryToken in
// production). It is empty until a phone connects.
type TokenStore interface {
	Get() string
	Adopt(token string)
}

// Hydrator pulls the D1 copy of the runtime state into the local state root
// after a phone hands over a verified token (REQ-046(4)). It runs once per
// process, before the first inbound message is processed.
type Hydrator interface {
	Hydrate(ctx context.Context) error
}

// Config wires the transport.
type Config struct {
	Listen       string // e.g. 127.0.0.1:18789 (the tunnel is the only public ingress)
	PublicListen string // e.g. 0.0.0.0:18789 for LAN-only operation
	Tunnel       bool
	Cloudflared  string
	Version      string
	Tokens       TokenStore
	MirrorInput  func(sdk.Input) func(context.Context) error
	Verifier     Verifier
	Hydrate      Hydrator
	History      HistoryStore                        // serves the phone's history from D1
	Announce     func(context.Context, string) error // publishes the tunnel URL (D1 `nodes`)
	InputBuffer  int
}

const defaultInputBuffer = 64

// subscriber is the write side a live phone connection exposes, narrowed so
// tests can observe the frames the gateway emits.
type subscriber interface {
	WriteMessage(int, []byte) error
	Close() error
}

// Version reports the label this build announces with, so the daemon can write
// it into the D1 `nodes` row without duplicating the config.
func (t *Transport) Version() string {
	if t == nil {
		return ""
	}
	return t.cfg.Version
}

// Transport is both the input source and the display for the mobile client.
type Transport struct {
	cfg      Config
	upgrader websocket.Upgrader
	gate     *Gate

	mu   sync.Mutex
	subs map[string]map[subscriber]struct{}
	seq  int64

	inputs chan sdk.Input

	hydrateOnce sync.Once
	hydratedErr error
}

func New(cfg Config) *Transport {
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:18789"
	}
	if cfg.InputBuffer <= 0 {
		cfg.InputBuffer = defaultInputBuffer
	}
	t := &Transport{
		cfg:    cfg,
		gate:   NewGate(GateConfig{Verify: cfg.Verifier, Cache: cfg.Tokens}),
		subs:   map[string]map[subscriber]struct{}{},
		inputs: make(chan sdk.Input, cfg.InputBuffer),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}
	return t
}

// Source makes the transport a RoutedDisplay so sdk.DispatchDisplay only routes
// mobile output here.
func (t *Transport) Source() string { return SourceName }

// Receive implements sdk.InputSource.
func (t *Transport) Receive(ctx context.Context) (<-chan sdk.Input, error) {
	return t.inputs, nil
}

// Display implements sdk.Display: one canonical sdk.Output becomes frames for
// every phone watching that session. A traced turn arrives as a trace stream
// (sdk.HarnessLoop skips its final output once a trace ran), so the answer is
// read from TraceResponseContent and TraceResponse closes the turn.
func (t *Transport) Display(ctx context.Context, output sdk.Output) error {
	if output.Source != "" && output.Source != SourceName {
		return nil
	}
	if output.Trace != nil {
		return t.displayTrace(output)
	}
	text := textOf(output)
	if text == "" {
		return nil
	}
	frame := Outbound{
		Kind:         FrameMessage,
		SessionID:    output.SessionID,
		Role:         "model",
		Agent:        agentFor(output),
		JobID:        output.Metadata[jobMetadataKey],
		Text:         text,
		Content:      []ContentPart{{Type: "text", Text: text}},
		InputTokens:  output.Response.Usage.InputTokens,
		OutputTokens: output.Response.Usage.OutputTokens,
	}
	t.broadcast(output.SessionID, frame)
	t.broadcast(output.SessionID, Outbound{Kind: FrameDone, SessionID: output.SessionID, JobID: frame.JobID})
	return nil
}

func (t *Transport) displayTrace(output sdk.Output) error {
	trace := *output.Trace
	agent := traceAgent(output)
	jobID := output.Metadata[jobMetadataKey]
	if sub := strings.TrimSpace(output.Metadata["trace_job_id"]); sub != "" {
		jobID = sub
	}
	switch trace.Stage {
	case sdk.TraceResponseText:
		return nil
	case sdk.TraceResponseContent:
		text := strings.TrimSpace(trace.Text)
		if text == "" && trace.Response != nil {
			text = textOfParts(toContent(trace.Response.Content))
		}
		if text == "" {
			return nil
		}
		var in, out int
		if trace.Response != nil {
			in, out = trace.Response.Usage.InputTokens, trace.Response.Usage.OutputTokens
		}
		t.broadcast(output.SessionID, Outbound{
			Kind:         FrameMessage,
			SessionID:    output.SessionID,
			Role:         "model",
			Agent:        agent,
			JobID:        jobID,
			Text:         text,
			Content:      []ContentPart{{Type: "text", Text: text}},
			InputTokens:  in,
			OutputTokens: out,
		})
		return nil
	case sdk.TraceResponse:
		t.broadcast(output.SessionID, Outbound{Kind: FrameDone, SessionID: output.SessionID, JobID: jobID})
		return nil
	default:
		text := strings.TrimSpace(sdk.TraceMessage(trace))
		if text == "" {
			return nil
		}
		t.broadcast(output.SessionID, Outbound{
			Kind:      FrameTrace,
			SessionID: output.SessionID,
			Role:      "system",
			Agent:     agent,
			JobID:     jobID,
			Stage:     string(trace.Stage),
			Text:      text,
		})
		return nil
	}
}

func traceAgent(output sdk.Output) string {
	if strings.EqualFold(strings.TrimSpace(output.Metadata["trace_actor"]), "subagent") {
		return "sub"
	}
	return "main"
}

// FinalText is the answer text a phone should keep for this output, whichever
// shape the harness delivered it in: a traced turn carries it on the trace.
func FinalText(output sdk.Output) string {
	if output.Trace != nil {
		if output.Trace.Stage == sdk.TraceResponseContent {
			if text := strings.TrimSpace(output.Trace.Text); text != "" {
				return text
			}
			if output.Trace.Response != nil {
				return strings.TrimSpace(textOfParts(toContent(output.Trace.Response.Content)))
			}
			return ""
		}
		return ""
	}
	return textOf(output)
}

const (
	agentMetadataKey = "mobile_agent"
	jobMetadataKey   = "mobile_job_id"
)

// PublishTrace streams a progress frame (tool calls, reasoning) to the session.
func (t *Transport) PublishTrace(sessionID, agent, jobID, stage, text string) {
	if sessionID == "" || text == "" {
		return
	}
	t.broadcast(sessionID, Outbound{
		Kind: FrameTrace, SessionID: sessionID, Role: "system", Agent: agent,
		JobID: jobID, Stage: stage, Text: text,
	})
}

// SendInput delivers one inbound frame to the harness.
func (t *Transport) SendInput(ctx context.Context, input sdk.Input) error {
	select {
	case t.inputs <- input:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops the transport and disconnects subscribers.
func (t *Transport) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for session, set := range t.subs {
		for c := range set {
			_ = c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "shutdown"))
			_ = c.Close()
		}
		delete(t.subs, session)
	}
}

// serveWS applies the handshake gate before upgrading.
func (t *Transport) serveWS(w http.ResponseWriter, r *http.Request) {
	decision := t.gate.Check(r)
	if !decision.Allowed {
		t.gate.Write(w, decision)
		return
	}
	// Verified: this is the daemon's only credential, held in memory (CON-012).
	t.cfg.Tokens.Adopt(decision.Token)

	conn, err := t.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	var sessionID string
	defer func() {
		if sessionID == "" {
			return
		}
		t.mu.Lock()
		if set := t.subs[sessionID]; set != nil {
			delete(set, conn)
			if len(set) == 0 {
				delete(t.subs, sessionID)
			}
		}
		t.mu.Unlock()
	}()

	conn.SetReadLimit(1 << 20)
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var in Inbound
		if err := json.Unmarshal(raw, &in); err != nil {
			t.send(conn, Outbound{Kind: FrameError, Text: "bad json"})
			return
		}
		if in.SessionID == "" {
			t.send(conn, Outbound{Kind: FrameError, Text: "session_id required"})
			return
		}
		sessionID = in.SessionID
		t.subscribe(sessionID, conn)

		if in.Type == FrameHello {
			// The first verified connection hydrates runtime state from D1
			// before any turn runs (REQ-046(4)).
			if t.cfg.Hydrate != nil {
				t.hydrateOnce.Do(func() { t.hydratedErr = t.cfg.Hydrate.Hydrate(r.Context()) })
				if t.hydratedErr != nil {
					log.Printf("mobile: hydrate from D1 failed: %v", t.hydratedErr)
					t.send(conn, Outbound{Kind: FrameError, Text: "ดึง state จาก D1 ไม่สำเร็จ: " + t.hydratedErr.Error()})
					return
				}
			}
			t.send(conn, Outbound{Kind: FrameAck, SessionID: sessionID, Role: "system", Stage: "resumed"})
			continue
		}
		if in.Type != FrameMessage {
			continue
		}
		text := textOfParts(in.Content)
		if text == "" {
			continue
		}
		t.send(conn, Outbound{Kind: FrameAck, SessionID: sessionID, Role: "system", ClientMsgID: in.ClientMsgID})
		input := sdk.Input{
			Source:    SourceName,
			SessionID: sessionID,
			Turn:      sdk.Turn{Role: sdk.RoleUser, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: text}}},
			Metadata:  map[string]string{"client_msg_id": in.ClientMsgID},
		}
		select {
		case t.inputs <- input:
			if t.cfg.MirrorInput != nil {
				if mirror := t.cfg.MirrorInput(input); mirror != nil {
					go func() {
						mirrorCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 45*time.Second)
						defer cancel()
						if err := mirror(mirrorCtx); err != nil {
							log.Printf("mobile: mirror user turn to D1: %v", err)
						}
					}()
				}
			}
		default:
			t.send(conn, Outbound{Kind: FrameError, SessionID: sessionID, Text: "busy: input queue full"})
		}
	}
}

func (t *Transport) subscribe(sessionID string, conn subscriber) {
	t.mu.Lock()
	defer t.mu.Unlock()
	set := t.subs[sessionID]
	if set == nil {
		set = map[subscriber]struct{}{}
		t.subs[sessionID] = set
	}
	set[conn] = struct{}{}
}

func (t *Transport) broadcast(sessionID string, frame Outbound) {
	if sessionID == "" {
		return
	}
	raw, err := json.Marshal(frame)
	if err != nil {
		return
	}
	t.mu.Lock()
	conns := make([]subscriber, 0, len(t.subs[sessionID]))
	for c := range t.subs[sessionID] {
		conns = append(conns, c)
	}
	t.mu.Unlock()
	for _, c := range conns {
		_ = c.WriteMessage(websocket.TextMessage, raw)
	}
}

func (t *Transport) send(conn *websocket.Conn, frame Outbound) {
	raw, err := json.Marshal(frame)
	if err != nil {
		return
	}
	_ = conn.WriteMessage(websocket.TextMessage, raw)
}

func textOf(output sdk.Output) string {
	if text := textOfParts(toContent(output.Content)); text != "" {
		return text
	}
	return strings.TrimSpace(textOfContent(output.Content))
}

func textOfContent(parts []sdk.ContentPart) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

func toContent(parts []sdk.ContentPart) []ContentPart {
	out := make([]ContentPart, 0, len(parts))
	for _, p := range parts {
		out = append(out, ContentPart{Type: string(p.Type), Text: p.Text})
	}
	return out
}

func textOfParts(parts []ContentPart) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

// agentFor prefers explicit transport metadata (set by the display adapter)
// and falls back to "main" for model output.
func agentFor(output sdk.Output) string {
	if agent := strings.TrimSpace(output.Metadata[agentMetadataKey]); agent != "" {
		return agent
	}
	return "main"
}

// StartHTTP serves the WebSocket endpoint and, when configured, opens a quick
// tunnel and announces the resulting public URL through the Worker.
func (t *Transport) StartHTTP(ctx context.Context, listen string) (func(), error) {
	if listen == "" {
		listen = t.cfg.Listen
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", t.serveWS)
	if t.cfg.History != nil {
		// One address for the phone: history comes through the tunnel and is
		// answered from D1 with the token the daemon already holds, so the app
		// only ever needs the tunnel URL plus its Cloudflare token (REQ-046(3)).
		mux.Handle("/api/", NewHistoryHandler(t.cfg.History, t.gate))
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	server := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("mobile: listen %s: %v", listen, err)
		}
	}()
	stopServer := func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}
	if !t.cfg.Tunnel {
		return stopServer, nil
	}
	port := portOfListen(listen)
	url, stopTunnel, err := RunQuickTunnel(ctx, port, t.cfg.Cloudflared)
	if err != nil {
		return stopServer, err
	}
	log.Printf("mobile: quick tunnel public URL: %s", url)
	t.announceLoop(ctx, url)
	return func() {
		stopTunnel()
		stopServer()
	}, nil
}

func (t *Transport) announceLoop(ctx context.Context, publicURL string) {
	if t.cfg.Announce == nil || t.cfg.Tokens == nil {
		return
	}
	announce := func() {
		if t.cfg.Tokens.Get() == "" {
			return // no phone connected yet: nothing to authenticate with
		}
		if err := t.cfg.Announce(ctx, publicURL); err != nil {
			log.Printf("mobile: announce tunnel: %v", err)
		}
	}
	go func() {
		announce()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				announce()
			}
		}
	}()
}
