package discord

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Tulipskun/ai/sdk"
	"github.com/bwmarrin/discordgo"
)

// discordMessage is the slice of a Discord MESSAGE_CREATE payload the transport
// cares about. Attachments carries the files reported on the message, so
// normalizeMessage can convert them instead of dropping them (REQ-025).
type discordMessage struct {
	ID          string
	ChannelID   string
	AuthorID    string
	AuthorName  string
	Content     string
	AuthorIsBot bool
	Attachments []InputAttachment
}

// normalizeMessage turns that slice into a canonical inbound message. Files are
// converted but not downloaded here: downloading belongs to the intake pump, so
// a slow CDN cannot block the gateway event handler.
func normalizeMessage(message discordMessage) (InputMessage, bool) {
	if message.AuthorIsBot || message.ID == "" || message.ChannelID == "" || message.AuthorID == "" {
		return InputMessage{}, false
	}
	return InputMessage{SessionID: "discord:channel:" + message.ChannelID, ChannelID: message.ChannelID, MessageID: message.ID, AuthorID: message.AuthorID, AuthorName: message.AuthorName, Content: message.Content, Attachments: convertAttachments(message.Attachments)}, true
}

// convertAttachments copies the attachment list behind a stable, transport-local
// shape. Nothing is filtered: an entry with no download location must still
// reach the intake pump, where it becomes a readable "not stored" note instead
// of vanishing from the message the user sent.
func convertAttachments(attachments []InputAttachment) []InputAttachment {
	if len(attachments) == 0 {
		return nil
	}
	out := make([]InputAttachment, len(attachments))
	copy(out, attachments)
	return out
}

// inputAttachments converts the wire struct Discord delivers. Both URL and
// ProxyURL are kept: the downloader prefers the proxy location, which is the
// channel-scoped one, and falls back to the raw CDN URL.
func inputAttachments(attachments []*discordgo.MessageAttachment) []InputAttachment {
	if len(attachments) == 0 {
		return nil
	}
	out := make([]InputAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		if attachment == nil {
			continue
		}
		out = append(out, InputAttachment{ID: attachment.ID, Name: attachment.Filename, ContentType: attachment.ContentType, Size: int64(attachment.Size), URL: attachment.URL, ProxyURL: attachment.ProxyURL})
	}
	return out
}

// gatewayIntents stays free of IntentsGuilds on purpose: attachment downloads go
// through the message's own proxy/CDN URL over REST, which needs no guild object
// and no guild subscription. Adding the intent would widen the privileged
// surface and the subscription the bot asks for without unlocking anything.
func gatewayIntents() discordgo.Intent {
	return discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages | discordgo.IntentsMessageContent
}

type tracePageState struct{ id, text, footer string }

type pendingTraceItem struct {
	text   string
	isText bool
}

type toolTraceState struct {
	pending         []pendingTraceItem
	textPages       []tracePageState
	messageID       string
	items           []string
	isText          bool
	dirty           bool
	lastPushMs      int64
	flushTimer      *time.Timer
	footerActive    bool
	turnStartMs     int64
	lastFooterSecMs int64
	turnUsage       sdk.Usage
	footerTimer     *time.Timer
}

const maxEmbedChars = 4000

// traceFlushInterval bounds embed updates to one snapshot per second so bursts of
// trace events stay under Discord's message-edit rate limits.
const traceFlushInterval = time.Second
const traceCooldownOnError = 5 * time.Second

// millisPerSecond is the millisecond unit for timestamp arithmetic: elapsed
// time is always nowMs-startMs, and update boundaries satisfy ts%1000 == 0.
const millisPerSecond = 1000

func embedChars(items []string) int {
	n := 0
	for i, w := range items {
		if i > 0 {
			n++
		}
		n += discordLength(w)
	}
	return n
}
func (s *toolTraceState) append(item string, isText bool) {
	if isText && s.isText && len(s.items) > 0 {
		s.items[len(s.items)-1] += item
	} else {
		s.items = append(s.items, item)
	}
	s.isText = isText
	for !s.isText && len(s.items) > 1 && embedChars(s.items) > maxEmbedChars {
		s.items = s.items[1:]
	}
}

// update rewrites the newest line that match identifies so live statuses (request
// accepted, tool result) reuse their placeholder line instead of stacking lines.
func (s *toolTraceState) update(item string, match func(string) bool) bool {
	if match == nil {
		return false
	}
	for i := len(s.items) - 1; i >= 0; i-- {
		if match(s.items[i]) {
			s.items[i] = item
			for !s.isText && len(s.items) > 1 && embedChars(s.items) > maxEmbedChars {
				s.items = s.items[1:]
			}
			return true
		}
	}
	return false
}

// reset drops the current lines so the next append starts a fresh embed.
func (s *toolTraceState) reset() { s.messageID = ""; s.items = nil; s.textPages = nil; s.dirty = false }

// nowMillis returns wall-clock time as Unix milliseconds. All turn timing is
// derived by subtracting these timestamps (nowMs-startMs), never time.Since.
func nowMillis() int64 { return time.Now().UnixMilli() }

// floorSecond drops the sub-second part so the result satisfies ts%1000 == 0.
func floorSecond(ts int64) int64 { return ts - ts%1000 }

// delayToNextSecond returns how long until the next whole-second boundary so
// periodic ticks land on timestamps with remainder 0.
func delayToNextSecond() time.Duration {
	if r := nowMillis() % 1000; r != 0 {
		return time.Duration(1000-r) * time.Millisecond
	}
	return time.Second
}
func traceFlushDelay(lastPushMs int64) time.Duration {
	if delayMs := millisPerSecond - (nowMillis() - lastPushMs); delayMs > 0 {
		return time.Duration(delayMs) * time.Millisecond
	}
	return 0
}

// AttachmentFileConfig is the file-transfer budget of the Discord transport,
// resolved from config/*.json by the caller (CON-001). Every zero field falls
// back to the transport default, so a caller that passes only what it configured
// still gets a bounded pipeline.
type AttachmentFileConfig struct {
	// DownloadTimeout bounds one attachment fetch.
	DownloadTimeout time.Duration
	// UploadTimeout bounds one multipart send. It is the transport's own budget,
	// deliberately separate from the harness display timeout.
	UploadTimeout time.Duration
	// MaxSendFileBytes and MaxSendFileCount bound one outbound message.
	MaxSendFileBytes int64
	MaxSendFileCount int
}

func (c AttachmentFileConfig) apply(a *Attachments) {
	if c.DownloadTimeout > 0 {
		a.DownloadTimeout = c.DownloadTimeout
	}
	if c.UploadTimeout > 0 {
		a.UploadTimeout = c.UploadTimeout
	}
	if c.MaxSendFileBytes > 0 {
		a.MaxSendFileBytes = c.MaxSendFileBytes
	}
	if c.MaxSendFileCount > 0 {
		a.MaxSendFileCount = c.MaxSendFileCount
	}
}

type Gateway struct {
	session          *discordgo.Session
	raw              chan InputMessage
	messages         chan InputMessage
	done             chan struct{}
	closeMu          sync.Mutex
	closed           bool
	attachments      *Attachments
	intake           sync.Once
	modelSettings    *ModelSettingsHandler
	providerSettings *ProviderSettingsHandler
	sessionCommand   *SessionCommandHandler
	newChannel       *NewChannelHandler
	sessionMapping   *SessionMapping
	resolveSession   func(context.Context, sdk.Input) (*sdk.Session, error)
	stop             func(string) bool
	v2Send           func(context.Context, string, []discordgo.MessageComponent) (string, error)
	v2Edit           func(context.Context, string, string, []discordgo.MessageComponent) error
	retryStatusMu    sync.Mutex
	retryStatus      map[string]string
	authorizedUserID string
	toolTraceMu      sync.Mutex
	toolTrace        map[string]*toolTraceState
}

// The gateway receives Discord events in per-event goroutines (discordgo
// dispatches each handler call with `go`), so messages of one channel can reach
// the handler out of order. Every message therefore goes through raw into a
// single pump goroutine, which stores its files and forwards it in arrival
// order: strictly better ordering than pushing straight into messages from
// concurrent handlers, and it keeps a slow CDN download off the websocket
// dispatch goroutine.
var (
	inboundBuffer = 256
)

func NewGateway(token string) (*Gateway, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("discord: bot token is required")
	}
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}
	gateway := &Gateway{session: session, raw: make(chan InputMessage, inboundBuffer), messages: make(chan InputMessage, inboundBuffer), done: make(chan struct{}), retryStatus: make(map[string]string), toolTrace: make(map[string]*toolTraceState), sessionMapping: NewSessionMapping()}
	session.Identify.Intents = gatewayIntents()
	session.AddHandler(func(_ *discordgo.Session, event *discordgo.MessageCreate) {
		if event == nil || event.Author == nil {
			return
		}
		message, ok := normalizeMessage(discordMessage{ID: event.ID, ChannelID: event.ChannelID, AuthorID: event.Author.ID, AuthorName: event.Author.Username, Content: event.Content, AuthorIsBot: event.Author.Bot, Attachments: inputAttachments(event.Attachments)})
		if !ok {
			return
		}
		message.SessionID = gateway.SessionIDForChannel(message.ChannelID)
		message.SessionID = gateway.routeSessionID(message.ChannelID, message.SessionID)
		gateway.resetToolTrace(message.ChannelID)
		// A new turn supersedes the outbound record of the previous one, so a
		// later turn can upload the same reference again.
		gateway.attachments.forgetSent(message.ChannelID)
		if gateway.stop != nil {
			_ = gateway.stop(message.SessionID)
		}
		select {
		case gateway.raw <- message:
		case <-gateway.done:
		}
	})
	session.AddHandler(func(s *discordgo.Session, event *discordgo.InteractionCreate) {
		if event == nil {
			return
		}
		if gateway.authorizedUserID != "" && event.Member != nil && event.Member.User != nil && event.Member.User.ID != gateway.authorizedUserID {
			_ = s.InteractionRespond(event.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "Unauthorized", Flags: discordgo.MessageFlagsEphemeral}})
			return
		}
		if gateway.authorizedUserID != "" && event.Member == nil && event.User != nil && event.User.ID != gateway.authorizedUserID {
			_ = s.InteractionRespond(event.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "Unauthorized", Flags: discordgo.MessageFlagsEphemeral}})
			return
		}
		if data, ok := event.Interaction.Data.(discordgo.ApplicationCommandInteractionData); ok && data.Name == "stop" && gateway.stop != nil {
			content := "ไม่มีงานที่กำลังทำอยู่"
			if gateway.stop(gateway.SessionIDForChannel(event.ChannelID)) {
				content = "หยุดการทำงานแล้ว"
			}
			_ = s.InteractionRespond(event.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: content, Flags: discordgo.MessageFlagsEphemeral}})
			return
		}
		if gateway.sessionCommand != nil {
			if err := gateway.sessionCommand.Handle(s, event); err != nil {
				log.Printf("discord: session command failed: %v", err)
				return
			}
			if data, ok := event.Interaction.Data.(discordgo.ApplicationCommandInteractionData); ok && data.Name == "session" {
				return
			}
		}
		if gateway.modelSettings != nil {
			if err := gateway.modelSettings.Handle(s, event); err != nil {
				log.Printf("discord: model settings failed: %v", err)
			}
			if event.Type == discordgo.InteractionApplicationCommand && event.ApplicationCommandData().Name == "model" {
				return
			}
		}
		if gateway.providerSettings != nil {
			if err := gateway.providerSettings.Handle(s, event); err != nil {
				log.Printf("discord: provider settings failed: %v", err)
			}
		}
		if gateway.newChannel != nil {
			if err := gateway.newChannel.Handle(s, event); err != nil {
				log.Printf("discord: new channel failed: %v", err)
				return
			}
			if data, ok := event.Interaction.Data.(discordgo.ApplicationCommandInteractionData); ok && data.Name == "new" {
				return
			}
		}
	})
	return gateway, nil
}
func (g *Gateway) ConfigureAuthorizedUser(userID string) {
	if g != nil {
		g.authorizedUserID = strings.TrimSpace(userID)
	}
}
func (g *Gateway) ConfigureModelSettings(handler *ModelSettingsHandler) {
	if g != nil {
		g.modelSettings = handler
	}
}
func (g *Gateway) ConfigureProviderSettings(handler *ProviderSettingsHandler) {
	if g != nil {
		g.providerSettings = handler
	}
}
func (g *Gateway) ConfigureNewChannel(handler *NewChannelHandler) {
	if g != nil {
		g.newChannel = handler
	}
}
func (g *Gateway) ConfigureSessionCommand(handler *SessionCommandHandler) {
	if g != nil {
		g.sessionCommand = handler
	}
}
func (g *Gateway) ConfigureSessionList(list func(int) ([]sdk.SessionInfo, error)) {
	if g != nil {
		g.sessionCommand = &SessionCommandHandler{ListSessions: list, SelectSession: g.SetChannelSession}
	}
}
func (g *Gateway) ConfigureSessionMapping(mapping *SessionMapping) {
	if g != nil && mapping != nil {
		g.sessionMapping = mapping
	}
}

// ConfigureSessionResolver lets the gateway route channel messages to the
// active agent side: a channel whose main session runs in sub mode talks to
// its sub session instead (REQ-030, CHANGE-021).
func (g *Gateway) ConfigureSessionResolver(resolve func(context.Context, sdk.Input) (*sdk.Session, error)) {
	if g != nil {
		g.resolveSession = resolve
	}
}

// routeSessionID returns the session a channel message belongs to: the
// channel's sub session while its main session runs in sub mode, else the
// main session. Resolution failures keep the main session.
func (g *Gateway) routeSessionID(channelID, mainSessionID string) string {
	if g == nil || g.resolveSession == nil {
		return mainSessionID
	}
	session, err := g.resolveSession(context.Background(), sdk.Input{SessionID: mainSessionID})
	if err != nil || session == nil {
		return mainSessionID
	}
	if session.Config().AgentMode == sdk.AgentModeSub {
		return mainSessionID + ":sub"
	}
	return mainSessionID
}
func (g *Gateway) SetChannelSession(channelID, sessionID string) error {
	if g == nil {
		return errors.New("discord: gateway is nil")
	}
	if g.sessionMapping == nil {
		g.sessionMapping = NewSessionMapping()
	}
	return g.sessionMapping.Set(channelID, sessionID)
}
func (g *Gateway) SessionIDForChannel(channelID string) string {
	if g == nil || g.sessionMapping == nil {
		return "discord:channel:" + strings.TrimSpace(channelID)
	}
	return g.sessionMapping.SessionID(channelID)
}
func (g *Gateway) ConfigureStop(handler func(string) bool) {
	if g != nil {
		g.stop = handler
	}
}

// ConfigureAttachments injects the file pipeline built on top of the attachment
// store opened from config/attachment.json (CON-011). Passing a nil store turns
// both directions off: inbound files still become reference notes in the text,
// but nothing is downloaded, which is the correct behaviour for a runtime with
// attachments disabled. cfg may be zero-valued to keep the transport defaults.
func (g *Gateway) ConfigureAttachments(store AttachmentStore, client *http.Client, cfg AttachmentFileConfig) {
	if g == nil {
		return
	}
	attachments := &Attachments{Store: store, Client: client}
	cfg.apply(attachments)
	g.attachments = attachments
}

// startIntake launches the pump that turns raw inbound messages into hydrated
// ones. It runs exactly once; both Start and Receive call it, because a test or
// an embedder may consume messages without ever opening the gateway.
func (g *Gateway) startIntake(ctx context.Context) {
	if g == nil {
		return
	}
	g.intake.Do(func() {
		go g.pumpIntake(ctx)
	})
}

// pumpIntake downloads and stores the attachments of each inbound message
// outside the Discord event handler, then forwards it. One goroutine per gateway
// is what keeps per-channel arrival order intact; a message with no files costs
// nothing but the hand-off.
func (g *Gateway) pumpIntake(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-g.done:
			return
		case message, ok := <-g.raw:
			if !ok {
				return
			}
			hydrated := g.attachments.Hydrate(ctx, message)
			select {
			case g.messages <- hydrated:
			case <-ctx.Done():
				return
			case <-g.done:
				return
			}
		}
	}
}
func (g *Gateway) Start(ctx context.Context) error {
	if g == nil || g.session == nil {
		return errors.New("discord: gateway is not initialized")
	}
	if g.authorizedUserID == "" {
		return errors.New("discord: authorized user ID is required")
	}
	// The intake pump must exist before the first event can arrive, so it starts
	// here as well as in Receive: an embedder may open the gateway and consume
	// inputs through either entry point.
	g.startIntake(ctx)
	if err := g.session.Open(); err != nil {
		return err
	}
	if g.modelSettings != nil {
		if err := g.registerCommand(&discordgo.ApplicationCommand{Name: "model", Description: "Configure the AI model for this session"}); err != nil {
			return err
		}
	}
	if g.providerSettings != nil {
		if err := g.registerCommand(&discordgo.ApplicationCommand{Name: "provider", Description: "Add or update an AI provider"}); err != nil {
			return err
		}
	}
	if g.sessionCommand != nil {
		if err := g.registerCommand(&discordgo.ApplicationCommand{Name: "session", Description: "Select an AI session"}); err != nil {
			return err
		}
	}
	if g.stop != nil {
		if err := g.registerCommand(&discordgo.ApplicationCommand{Name: "stop", Description: "Stop the current AI task"}); err != nil {
			return err
		}
	}
	if g.newChannel != nil {
		if err := g.registerCommand(&discordgo.ApplicationCommand{Name: "new", Description: "Create a new channel with this channel's model settings"}); err != nil {
			return err
		}
	}
	go func() { <-ctx.Done(); _ = g.Close(context.Background()) }()
	return nil
}
func (g *Gateway) registerCommand(command *discordgo.ApplicationCommand) error {
	commands, err := g.session.ApplicationCommands(g.session.State.User.ID, "")
	if err != nil {
		return err
	}
	for _, existing := range commands {
		if existing.Name == command.Name {
			_, err = g.session.ApplicationCommandEdit(g.session.State.User.ID, "", existing.ID, command)
			return err
		}
	}
	_, err = g.session.ApplicationCommandCreate(g.session.State.User.ID, "", command)
	return err
}
func (g *Gateway) Receive(ctx context.Context) (<-chan sdk.Input, error) {
	if g == nil || g.messages == nil {
		return nil, errors.New("discord: input source is not initialized")
	}
	g.startIntake(ctx)
	return InputSource{Messages: g.messages}.Receive(ctx)
}
func (g *Gateway) SendMessage(ctx context.Context, channelID, content string) error {
	if g == nil || g.session == nil {
		return errors.New("discord: gateway is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if channelID == "" || content == "" {
		return errors.New("discord: channel ID and content are required")
	}
	_, err := g.session.ChannelMessageSend(channelID, content)
	return err
}

// SendFiles delivers one message with attachments through Discord's multipart
// endpoint. It is the outbound half of REQ-026 and lives here, in the Discord
// module, because the wire format is a transport concern: the agent core only
// ever sees a reference (CON-004).
//
// The callers pass an upload context that already carries the transport's own
// timeout, so this method only checks it; it does not impose a text-reply budget
// on a file transfer.
func (g *Gateway) SendFiles(ctx context.Context, channelID, content string, files []*discordgo.File) error {
	if g == nil || g.session == nil {
		return errors.New("discord: gateway is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if channelID == "" {
		return errors.New("discord: channel ID is required")
	}
	if len(files) == 0 {
		return errors.New("discord: at least one file is required")
	}
	_, err := g.session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Content: content, Files: files}, discordgo.WithContext(ctx))
	return err
}

func (g *Gateway) SendStatusMessage(ctx context.Context, channelID, content string) (string, error) {
	if g == nil || g.session == nil {
		return "", errors.New("discord: gateway is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	message, err := g.session.ChannelMessageSend(channelID, content)
	if err != nil {
		return "", err
	}
	return message.ID, nil
}
func (g *Gateway) EditMessage(ctx context.Context, channelID, messageID, content string) error {
	if g == nil || g.session == nil {
		return errors.New("discord: gateway is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if channelID == "" || messageID == "" {
		return errors.New("discord: channel ID and message ID are required")
	}
	_, err := g.session.ChannelMessageEdit(channelID, messageID, content)
	return err
}
func (g *Gateway) DeleteMessage(ctx context.Context, channelID, messageID string) error {
	if g == nil || g.session == nil {
		return errors.New("discord: gateway is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if channelID == "" || messageID == "" {
		return errors.New("discord: channel ID and message ID are required")
	}
	return g.session.ChannelMessageDelete(channelID, messageID)
}
func (g *Gateway) SendEmbed(ctx context.Context, channelID string, embed *discordgo.MessageEmbed) (string, error) {
	if g == nil || g.session == nil {
		return "", errors.New("discord: gateway is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	message, err := g.session.ChannelMessageSendEmbed(channelID, embed)
	if err != nil {
		return "", err
	}
	return message.ID, nil
}
func (g *Gateway) EditEmbed(ctx context.Context, channelID, messageID string, embed *discordgo.MessageEmbed) error {
	if g == nil || g.session == nil {
		return errors.New("discord: gateway is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if channelID == "" || messageID == "" {
		return errors.New("discord: channel ID and message ID are required")
	}
	_, err := g.session.ChannelMessageEditEmbed(channelID, messageID, embed)
	return err
}

// SendComponentsV2 posts a Components V2 message (no embeds, no content —
// the flag disables both) and returns its ID (REQ-022, REQ-031).
func (g *Gateway) SendComponentsV2(ctx context.Context, channelID string, components []discordgo.MessageComponent) (string, error) {
	if g == nil {
		return "", errors.New("discord: gateway is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if g.v2Send != nil {
		return g.v2Send(ctx, channelID, components)
	}
	if g.session == nil {
		return "", errors.New("discord: gateway is not initialized")
	}
	message, err := g.session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Components: components, Flags: discordgo.MessageFlagsIsComponentsV2})
	if err != nil {
		return "", err
	}
	return message.ID, nil
}

// EditComponentsV2 rewrites a Components V2 message in place, keeping the V2
// flag on every edit so the layout survives (REQ-022, REQ-031).
func (g *Gateway) EditComponentsV2(ctx context.Context, channelID, messageID string, components []discordgo.MessageComponent) error {
	if g == nil {
		return errors.New("discord: gateway is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if channelID == "" || messageID == "" {
		return errors.New("discord: channel ID and message ID are required")
	}
	if g.v2Edit != nil {
		return g.v2Edit(ctx, channelID, messageID, components)
	}
	if g.session == nil {
		return errors.New("discord: gateway is not initialized")
	}
	_, err := g.session.ChannelMessageEditComplex(&discordgo.MessageEdit{Channel: channelID, ID: messageID, Components: &components, Flags: discordgo.MessageFlagsIsComponentsV2})
	return err
}
func toolTraceEmbed(items []string, footer string) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{Description: strings.Join(items, "\n")}
	if footer != "" {
		embed.Footer = &discordgo.MessageEmbedFooter{Text: footer}
	}
	return embed
}
func turnFooterText(state *toolTraceState) string {
	if state == nil || state.turnStartMs == 0 {
		return ""
	}
	elapsedMs := nowMillis() - state.turnStartMs
	if elapsedMs < 0 {
		elapsedMs = 0
	}
	return "in: " + formatCount(state.turnUsage.InputTokens) + "/" + formatCount(state.turnUsage.CacheReadTokens) + " · out: " + formatCount(state.turnUsage.OutputTokens) + " · ⏱ " + formatElapsed(time.Duration(elapsedMs)*time.Millisecond)
}
func (g *Gateway) traceState(channelID string) *toolTraceState {
	if g.toolTrace == nil {
		g.toolTrace = make(map[string]*toolTraceState)
	}
	state := g.toolTrace[channelID]
	if state == nil {
		state = &toolTraceState{}
		g.toolTrace[channelID] = state
	}
	return state
}
func (g *Gateway) setToolTrace(_ context.Context, channelID string, items []string) error {
	g.toolTraceMu.Lock()
	defer g.toolTraceMu.Unlock()
	state := g.traceState(channelID)
	state.items = append([]string(nil), items...)
	state.isText = false
	g.scheduleTraceFlushLocked(channelID, state)
	return nil
}
func (g *Gateway) appendToolTrace(ctx context.Context, channelID, item string) error {
	return g.appendTraceItem(ctx, channelID, truncateOneLine(item, maxToolTraceLength), false)
}
func (g *Gateway) appendTextTrace(ctx context.Context, channelID, text string) error {
	return g.appendTraceItem(ctx, channelID, text, true)
}
func (g *Gateway) updateToolTrace(ctx context.Context, channelID, item string, match func(string) bool) error {
	return g.updateTraceItem(ctx, channelID, truncateOneLine(item, maxToolTraceLength), false, match)
}
func (g *Gateway) appendTraceItem(ctx context.Context, channelID, item string, isText bool) error {
	g.toolTraceMu.Lock()
	defer g.toolTraceMu.Unlock()
	state := g.traceState(channelID)
	if err := g.appendStateItemLocked(ctx, channelID, state, item, isText); err != nil {
		return err
	}
	g.scheduleTraceFlushLocked(channelID, state)
	return nil
}
func (g *Gateway) updateTraceItem(ctx context.Context, channelID, item string, isText bool, match func(string) bool) error {
	g.toolTraceMu.Lock()
	defer g.toolTraceMu.Unlock()
	state := g.traceState(channelID)
	if len(state.pending) == 0 && !state.isText && state.update(item, match) {
		g.scheduleTraceFlushLocked(channelID, state)
		return nil
	}
	if err := g.appendStateItemLocked(ctx, channelID, state, item, isText); err != nil {
		return err
	}
	g.scheduleTraceFlushLocked(channelID, state)
	return nil
}
func (g *Gateway) appendStateItemLocked(ctx context.Context, channelID string, state *toolTraceState, item string, isText bool) error {
	// Trace callbacks are not replayed after display errors. Queue the incoming
	// item before flushing so a failed transition cannot discard a text delta.
	state.pending = append(state.pending, pendingTraceItem{item, isText})
	return g.drainTraceItemsLocked(ctx, channelID, state)
}

func (g *Gateway) drainTraceItemsLocked(ctx context.Context, channelID string, state *toolTraceState) error {
	for len(state.pending) > 0 {
		item := state.pending[0]
		if len(state.items) > 0 && state.isText != item.isText {
			if err := g.pushTraceSnapshotLocked(ctx, channelID, state); err != nil {
				return err
			}
			state.reset()
		}
		state.append(item.text, item.isText)
		state.dirty = true
		state.pending = state.pending[1:]
	}
	return nil
}

// scheduleTraceFlushLocked queues a snapshot of the current trace state; snapshots
// are pushed at most once per traceFlushInterval.
func (g *Gateway) scheduleTraceFlushLocked(channelID string, state *toolTraceState) {
	state.dirty = true
	if state.flushTimer != nil {
		return
	}
	state.flushTimer = time.AfterFunc(traceFlushDelay(state.lastPushMs), func() {
		if err := g.flushToolTrace(context.Background(), channelID); err != nil {
			log.Printf("discord: background trace flush failed: %v", err)
		}
	})
}

// flushToolTrace pushes the current trace snapshot to Discord immediately.
func (g *Gateway) flushToolTrace(ctx context.Context, channelID string) error {
	if g == nil {
		return nil
	}
	g.toolTraceMu.Lock()
	defer g.toolTraceMu.Unlock()
	state := g.toolTrace[channelID]
	if state == nil {
		return nil
	}
	return g.pushToolTraceLocked(ctx, channelID, state)
}
func (g *Gateway) pushToolTraceLocked(ctx context.Context, channelID string, state *toolTraceState) error {
	if err := g.drainTraceItemsLocked(ctx, channelID, state); err != nil {
		return err
	}
	return g.pushTraceSnapshotLocked(ctx, channelID, state)
}

func (g *Gateway) pushTraceSnapshotLocked(ctx context.Context, channelID string, state *toolTraceState) error {
	if state.flushTimer != nil {
		state.flushTimer.Stop()
		state.flushTimer = nil
	}
	if !state.dirty || len(state.items) == 0 {
		return nil
	}
	var err error
	if state.isText {
		err = g.pushTextPagesLocked(ctx, channelID, state)
	} else {
		id := state.messageID
		err = g.pushEmbedPage(ctx, channelID, &id, toolTraceEmbed(state.items, turnFooterText(state)))
		state.messageID = id
	}
	if err != nil {
		state.dirty = true
		state.flushTimer = time.AfterFunc(traceCooldownOnError, func() {
			if ferr := g.flushToolTrace(context.Background(), channelID); ferr != nil {
				log.Printf("discord: background trace flush failed: %v", ferr)
			}
		})
		return err
	}
	state.dirty = false
	state.lastPushMs = nowMillis()
	return nil
}

func (g *Gateway) pushEmbedPage(ctx context.Context, channelID string, id *string, embed *discordgo.MessageEmbed) error {
	if *id != "" {
		err := g.EditEmbed(ctx, channelID, *id, embed)
		if err == nil {
			return nil
		}
		if !isUnknownMessage(err) {
			return err
		}
	}
	sent, err := g.SendEmbed(ctx, channelID, embed)
	if err != nil {
		return err
	}
	*id = sent
	return nil
}

func (g *Gateway) pushTextPagesLocked(ctx context.Context, channelID string, state *toolTraceState) error {
	pages := paginateDiscord(strings.Join(state.items, ""), maxEmbedChars)
	for i, page := range pages {
		if i >= len(state.textPages) {
			state.textPages = append(state.textPages, tracePageState{})
		}
		sent := &state.textPages[i]
		footer := ""
		if i == len(pages)-1 {
			footer = turnFooterText(state)
		}
		if sent.id != "" && sent.text == page.text && sent.footer == footer {
			continue
		}
		if err := g.pushEmbedPage(ctx, channelID, &sent.id, toolTraceEmbed([]string{page.text}, footer)); err != nil {
			return err
		}
		sent.text = page.text
		sent.footer = footer
	}
	// A partial fence delimiter can change page layout as more stream text arrives.
	for len(state.textPages) > len(pages) {
		i := len(state.textPages) - 1
		if err := g.DeleteMessage(ctx, channelID, state.textPages[i].id); err != nil && !isUnknownMessage(err) {
			return err
		}
		state.textPages = state.textPages[:i]
	}
	return nil
}

func (g *Gateway) resetToolTrace(channelID string) {
	if g == nil || strings.TrimSpace(channelID) == "" {
		return
	}
	g.toolTraceMu.Lock()
	defer g.toolTraceMu.Unlock()
	if state := g.toolTrace[channelID]; state != nil {
		if state.footerTimer != nil {
			state.footerTimer.Stop()
		}
		if state.flushTimer != nil {
			state.flushTimer.Stop()
		}
	}
	delete(g.toolTrace, channelID)
}
func (g *Gateway) startTurnFooter(channelID string) {
	if g == nil || strings.TrimSpace(channelID) == "" {
		return
	}
	g.toolTraceMu.Lock()
	defer g.toolTraceMu.Unlock()
	state := g.traceState(channelID)
	if !state.footerActive {
		state.footerActive = true
		state.turnStartMs = nowMillis()
		state.lastFooterSecMs = 0
	}
	g.scheduleFooterTickLocked(channelID, state)
}
func (g *Gateway) updateTurnFooterUsage(channelID string, usage sdk.Usage) {
	if g == nil || strings.TrimSpace(channelID) == "" {
		return
	}
	g.toolTraceMu.Lock()
	defer g.toolTraceMu.Unlock()
	g.traceState(channelID).turnUsage = usage
}
func (g *Gateway) stopTurnFooter(channelID string) {
	if g == nil || strings.TrimSpace(channelID) == "" {
		return
	}
	g.toolTraceMu.Lock()
	defer g.toolTraceMu.Unlock()
	state := g.toolTrace[channelID]
	if state == nil {
		return
	}
	state.footerActive = false
	if state.footerTimer != nil {
		state.footerTimer.Stop()
		state.footerTimer = nil
	}
	if len(state.items) > 0 {
		state.dirty = true
		g.scheduleTraceFlushLocked(channelID, state)
	}
}
func (g *Gateway) scheduleFooterTickLocked(channelID string, state *toolTraceState) {
	if state == nil || !state.footerActive || state.footerTimer != nil {
		return
	}
	state.footerTimer = time.AfterFunc(delayToNextSecond(), func() { g.tickTurnFooter(channelID) })
}
func (g *Gateway) tickTurnFooter(channelID string) {
	if g == nil {
		return
	}
	g.toolTraceMu.Lock()
	defer g.toolTraceMu.Unlock()
	state := g.toolTrace[channelID]
	if state == nil {
		return
	}
	state.footerTimer = nil
	if !state.footerActive {
		return
	}
	sec := floorSecond(nowMillis())
	if sec == state.lastFooterSecMs {
		g.scheduleFooterTickLocked(channelID, state)
		return
	}
	state.lastFooterSecMs = sec
	state.dirty = true
	g.scheduleTraceFlushLocked(channelID, state)
	g.scheduleFooterTickLocked(channelID, state)
}
func isUnknownMessage(err error) bool {
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) {
		return restErr.Message != nil && restErr.Message.Code == 10008
	}
	return false
}
func (g *Gateway) clearToolTrace(_ context.Context, _ string) error { return nil }
func (g *Gateway) updateRetryStatus(ctx context.Context, channelID, content string) error {
	g.retryStatusMu.Lock()
	defer g.retryStatusMu.Unlock()
	if messageID := g.retryStatus[channelID]; messageID != "" {
		editErr := g.EditMessage(ctx, channelID, messageID, content)
		if editErr == nil {
			return nil
		}
		if !isUnknownMessage(editErr) {
			return editErr
		}
		delete(g.retryStatus, channelID)
	}
	messageID, err := g.SendStatusMessage(ctx, channelID, content)
	if err != nil {
		return err
	}
	g.retryStatus[channelID] = messageID
	return nil
}
func (g *Gateway) clearRetryStatus(ctx context.Context, channelID string) error {
	g.retryStatusMu.Lock()
	defer g.retryStatusMu.Unlock()
	messageID := g.retryStatus[channelID]
	if messageID == "" {
		return nil
	}
	if err := g.DeleteMessage(ctx, channelID, messageID); err != nil && !isUnknownMessage(err) {
		return err
	}
	delete(g.retryStatus, channelID)
	return nil
}
func (g *Gateway) Display(ctx context.Context, output sdk.Output) error {
	return (Display{Sender: g, Attachments: g.attachments}).Display(ctx, output)
}
func (g *Gateway) Source() string { return "discord" }
func (g *Gateway) Close(_ context.Context) error {
	if g == nil || g.session == nil {
		return nil
	}
	g.closeMu.Lock()
	if g.closed {
		g.closeMu.Unlock()
		return nil
	}
	g.closed = true
	close(g.done)
	g.closeMu.Unlock()
	g.toolTraceMu.Lock()
	for _, state := range g.toolTrace {
		if state.flushTimer != nil {
			state.flushTimer.Stop()
			state.flushTimer = nil
		}
		if state.footerTimer != nil {
			state.footerTimer.Stop()
			state.footerTimer = nil
		}
	}
	g.toolTraceMu.Unlock()
	return g.session.Close()
}
