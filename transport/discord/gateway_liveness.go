package discord

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Gateway liveness (REQ-044): track the Discord websocket connection so an
// offline-while-daemon-alive outage is detected and repaired without a daemon
// restart. V2 display, pagination, throttles, and routing are untouched.
//
// Design:
//   - Ready/Resumed mark the gateway connected; Disconnect marks it offline.
//     Any MessageCreate/InteractionCreate also counts as proof of life.
//   - lastEventMs is the last proof-of-life timestamp (UnixMilli).
//   - A heartbeat timestamp file under the state root exposes bot-connection
//     liveness to the operator even when the daemon pid is alive.
//   - A watchdog goroutine refreshes the heartbeat file while connected and
//     reopens the session with exponential backoff when silence exceeds the
//     threshold or the connection flag is down.

const (
	// gatewayWatchdogInterval paces the watchdog tick. It only observes and
	// refreshes the heartbeat file; it never sends Discord messages.
	gatewayWatchdogInterval = 30 * time.Second
	// gatewaySilenceThreshold is how long without any gateway proof-of-life
	// before the watchdog attempts a reopen.
	gatewaySilenceThreshold = 3 * time.Minute
	// gatewayHeartbeatWriteMinInterval throttles heartbeat file writes during
	// message bursts so a busy channel does not churn the disk.
	gatewayHeartbeatWriteMinInterval = 10 * time.Second
	// gatewayReconnectMaxAttempts bounds one watchdog-triggered reopen.
	gatewayReconnectMaxAttempts = 5
)

// gatewayReconnectBaseDelay is the first backoff delay; it doubles per
// attempt (1s, 2s, 4s, 8s, 16s). A var so tests can shrink it.
var gatewayReconnectBaseDelay = time.Second

// ConfigureHeartbeatPath sets the bot-connectivity timestamp file the gateway
// writes on proof-of-life. An empty path disables file writes (offline tests,
// CLI-only runs). The daemon passes <state>/discord.heartbeat.
func (g *Gateway) ConfigureHeartbeatPath(path string) {
	if g == nil {
		return
	}
	g.livenessMu.Lock()
	defer g.livenessMu.Unlock()
	g.heartbeatPath = path
}

// ConfigureReopenHook injects the reopen function used by the watchdog.
// Production uses session Close+Open; tests inject a stub. A nil hook
// restores the default behavior.
func (g *Gateway) ConfigureReopenHook(fn func() error) {
	if g == nil {
		return
	}
	g.livenessMu.Lock()
	defer g.livenessMu.Unlock()
	g.reopenGatewayFunc = fn
}

// Connected reports the last known Discord websocket state: true after
// Ready/Resumed or any inbound event, false after Disconnect/Close or before
// the first Ready. It is a flag, not a fresh probe.
func (g *Gateway) Connected() bool {
	if g == nil {
		return false
	}
	g.livenessMu.RLock()
	defer g.livenessMu.RUnlock()
	return g.connected
}

// LastEventMs returns the last gateway proof-of-life timestamp (UnixMilli),
// or 0 when no event has been seen yet.
func (g *Gateway) LastEventMs() int64 {
	if g == nil {
		return 0
	}
	g.livenessMu.RLock()
	defer g.livenessMu.RUnlock()
	return g.lastEventMs
}

// markGatewayEvent records proof-of-life at nowMs. When alive is true the
// gateway counts as connected; Disconnect passes false. The heartbeat file
// write is throttled to gatewayHeartbeatWriteMinInterval.
func (g *Gateway) markGatewayEvent(alive bool, nowMs int64) {
	if g == nil {
		return
	}
	var path string
	var lastWrite int64
	g.livenessMu.Lock()
	g.lastEventMs = nowMs
	g.connected = alive
	path = g.heartbeatPath
	lastWrite = g.lastHeartbeatMs
	g.livenessMu.Unlock()
	if !alive || path == "" {
		return
	}
	if nowMs-lastWrite < gatewayHeartbeatWriteMinInterval.Milliseconds() && lastWrite != 0 {
		return
	}
	g.writeHeartbeatFile(path, nowMs)
}

// writeHeartbeatFile stores nowMs as plain UnixMilli text, creating the
// parent directory. Failures are logged, never returned: liveness must not
// break message intake or display.
func (g *Gateway) writeHeartbeatFile(path string, nowMs int64) {
	if g == nil || path == "" {
		return
	}
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	if err := os.WriteFile(path, []byte(itoaMs(nowMs)), 0o644); err != nil {
		log.Printf("discord: heartbeat write failed: %v", err)
		return
	}
	g.livenessMu.Lock()
	g.lastHeartbeatMs = nowMs
	g.livenessMu.Unlock()
}

// itoaMs formats UnixMilli without importing strconv into the hot path
// callers; kept tiny and dependency-free.
func itoaMs(ms int64) string {
	if ms == 0 {
		return "0"
	}
	neg := ms < 0
	if neg {
		ms = -ms
	}
	var buf [32]byte
	i := len(buf)
	for ms > 0 {
		i--
		buf[i] = byte('0' + ms%10)
		ms /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// onGatewayReady marks the session connected. Registered via AddHandler.
func (g *Gateway) onGatewayReady(_ *discordgo.Session, event *discordgo.Ready) {
	if event == nil {
		return
	}
	now := nowMillis()
	g.markGatewayEvent(true, now)
	log.Printf("discord: gateway ready (lastEventMs=%d)", now)
}

// onGatewayResumed marks the session connected after a resume.
func (g *Gateway) onGatewayResumed(_ *discordgo.Session, event *discordgo.Resumed) {
	if event == nil {
		return
	}
	now := nowMillis()
	g.markGatewayEvent(true, now)
	log.Printf("discord: gateway resumed (lastEventMs=%d)", now)
}

// onGatewayDisconnect marks the session offline; the watchdog will reopen.
func (g *Gateway) onGatewayDisconnect(_ *discordgo.Session, _ *discordgo.Disconnect) {
	g.livenessMu.Lock()
	g.connected = false
	g.livenessMu.Unlock()
	log.Printf("discord: gateway disconnected, watchdog will reopen")
}

// silenceDuration reports how long ago the last proof-of-life was seen.
func (g *Gateway) silenceDuration(now time.Time) time.Duration {
	if g == nil {
		return 0
	}
	g.livenessMu.RLock()
	last := g.lastEventMs
	connected := g.connected
	sess := g.session
	g.livenessMu.RUnlock()
	// A recent discordgo heartbeat ack also counts as proof-of-life, so an
	// idle-but-connected bot (no messages for hours) is not mistaken for a
	// dead socket. The ack timestamp lives on the session, outside our lock.
	if sess != nil && connected {
		if ack := sess.LastHeartbeatAck; !ack.IsZero() {
			if ackMs := ack.UnixMilli(); ackMs > last {
				last = ackMs
			}
		}
	}
	if last == 0 {
		return 0
	}
	d := now.UnixMilli() - last
	if d < 0 {
		return 0
	}
	return time.Duration(d) * time.Millisecond
}

// shouldReopen reports whether the watchdog must attempt a reopen: the
// connection flag is down, or silence exceeded the threshold once an initial
// event was seen. A zero lastEventMs (never connected) never triggers a
// watchdog reopen; Start's Open error path owns that case.
func (g *Gateway) shouldReopen(now time.Time) bool {
	if g == nil {
		return false
	}
	g.livenessMu.RLock()
	connected := g.connected
	last := g.lastEventMs
	g.livenessMu.RUnlock()
	if !connected {
		return last != 0
	}
	if last == 0 {
		return false
	}
	return g.silenceDuration(now) >= gatewaySilenceThreshold
}

// startGatewayWatchdog launches the reconnect loop exactly once. It refreshes
// the heartbeat file while connected and reopens the session with backoff on
// silence. It exits on ctx done, gateway Close, or a closed session.
func (g *Gateway) startGatewayWatchdog(ctx context.Context) {
	if g == nil {
		return
	}
	g.watchdogOnce.Do(func() {
		go g.watchdogLoop(ctx)
	})
}

// watchdogLoop is the goroutine body behind startGatewayWatchdog.
func (g *Gateway) watchdogLoop(ctx context.Context) {
	ticker := time.NewTicker(gatewayWatchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-g.done:
			return
		case now := <-ticker.C:
			g.checkGatewayLiveness(now)
		}
	}
}

// checkGatewayLiveness is one watchdog tick: refresh the heartbeat file while
// connected, then reopen with backoff when shouldReopen fires.
func (g *Gateway) checkGatewayLiveness(now time.Time) {
	if g == nil {
		return
	}
	select {
	case <-g.done:
		return
	default:
	}
	if g.Connected() {
		// Keep the heartbeat file fresh during idle-but-connected hours.
		// markGatewayEvent throttles the actual write.
		g.livenessMu.RLock()
		path := g.heartbeatPath
		g.livenessMu.RUnlock()
		if path != "" {
			g.writeHeartbeatFileThrottled(now.UnixMilli())
		}
	}
	if !g.shouldReopen(now) {
		return
	}
	silence := g.silenceDuration(now)
	log.Printf("discord: gateway silence %s exceeds %s, reopening", silence, gatewaySilenceThreshold)
	if err := g.reopenWithBackoff(); err != nil {
		log.Printf("discord: gateway reopen failed after %d attempts: %v", gatewayReconnectMaxAttempts, err)
		return
	}
	log.Printf("discord: gateway reopened after silence %s", silence)
}

// writeHeartbeatFileThrottled writes only when the min interval elapsed.
func (g *Gateway) writeHeartbeatFileThrottled(nowMs int64) {
	g.livenessMu.RLock()
	path := g.heartbeatPath
	lastWrite := g.lastHeartbeatMs
	g.livenessMu.RUnlock()
	if path == "" {
		return
	}
	if lastWrite != 0 && nowMs-lastWrite < gatewayHeartbeatWriteMinInterval.Milliseconds() {
		return
	}
	g.writeHeartbeatFile(path, nowMs)
}

// reopenWithBackoff reopens the Discord session with exponential backoff.
// The hook (tests) wins; otherwise it closes and reopens the live session.
func (g *Gateway) reopenWithBackoff() error {
	g.livenessMu.RLock()
	hook := g.reopenGatewayFunc
	g.livenessMu.RUnlock()
	if hook != nil {
		return reopenWithBackoffCall(hook)
	}
	return reopenWithBackoffCall(g.defaultReopen)
}

// reopenWithBackoffCall runs fn up to gatewayReconnectMaxAttempts with
// doubling delays. A nil fn is a no-op success for offline gateways.
func reopenWithBackoffCall(fn func() error) error {
	if fn == nil {
		return nil
	}
	delay := gatewayReconnectBaseDelay
	var err error
	for attempt := 1; attempt <= gatewayReconnectMaxAttempts; attempt++ {
		if err = fn(); err == nil {
			return nil
		}
		log.Printf("discord: gateway reopen attempt %d/%d failed: %v", attempt, gatewayReconnectMaxAttempts, err)
		if attempt < gatewayReconnectMaxAttempts {
			time.Sleep(delay)
			delay *= 2
		}
	}
	return err
}

// defaultReopen closes and reopens the live discordgo session, then marks
// proof-of-life on success so silence resets immediately.
func (g *Gateway) defaultReopen() error {
	if g == nil || g.session == nil {
		return nil
	}
	// Close stops the old websocket/heartbeat goroutines; Open dials fresh.
	// A Close error is ignored: the socket may already be dead.
	_ = g.session.Close()
	if err := g.session.Open(); err != nil {
		return err
	}
	g.markGatewayEvent(true, nowMillis())
	return nil
}
