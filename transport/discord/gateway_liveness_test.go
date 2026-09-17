package discord

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestGatewayLivenessReadyResumedConnected(t *testing.T) {
	gateway, err := NewGateway("token")
	if err != nil {
		t.Fatal(err)
	}
	if gateway.Connected() {
		t.Fatal("fresh gateway must not be connected")
	}
	if gateway.LastEventMs() != 0 {
		t.Fatalf("fresh gateway lastEvent = %d, want 0", gateway.LastEventMs())
	}
	gateway.onGatewayReady(nil, &discordgo.Ready{})
	if !gateway.Connected() {
		t.Fatal("Ready must mark gateway connected")
	}
	if gateway.LastEventMs() == 0 {
		t.Fatal("Ready must stamp last-event time")
	}
	gateway.onGatewayDisconnect(nil, &discordgo.Disconnect{})
	if gateway.Connected() {
		t.Fatal("Disconnect must mark gateway offline")
	}
	gateway.onGatewayResumed(nil, &discordgo.Resumed{})
	if !gateway.Connected() {
		t.Fatal("Resumed must mark gateway connected")
	}
	if gateway.LastEventMs() == 0 {
		t.Fatal("Resumed must stamp last-event time")
	}
	// Nil events must not move state.
	gateway.onGatewayReady(nil, nil)
	gateway.onGatewayResumed(nil, nil)
	if !gateway.Connected() {
		t.Fatal("nil Ready/Resumed must not clear connected state")
	}
}

func TestGatewayLivenessHeartbeatFileWrite(t *testing.T) {
	gateway, err := NewGateway("token")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "discord.heartbeat")
	gateway.ConfigureHeartbeatPath(path)
	gateway.markGatewayEvent(true, nowMillis())
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("heartbeat file must exist: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("heartbeat file must not be empty")
	}
}

func TestGatewayLivenessWatchdogReopensOnSilence(t *testing.T) {
	gateway, err := NewGateway("token")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	gateway.ConfigureReopenHook(func() error {
		calls++
		gateway.markGatewayEvent(true, nowMillis())
		return nil
	})
	// Simulate a connection that went quiet 10 minutes ago (both the
	// gateway stamp and the discordgo heartbeat ack, which silenceDuration
	// also counts as proof-of-life for idle-but-connected bots).
	old := time.Now().Add(-10 * time.Minute).UnixMilli()
	gateway.livenessMu.Lock()
	gateway.connected = true
	gateway.lastEventMs = old
	gateway.livenessMu.Unlock()
	gateway.session.LastHeartbeatAck = time.Now().Add(-10 * time.Minute).UTC()
	if !gateway.shouldReopen(time.Now()) {
		t.Fatal("10-minute silence must trigger reopen")
	}
	gateway.checkGatewayLiveness(time.Now())
	if calls != 1 {
		t.Fatalf("reopen calls = %d, want 1", calls)
	}
	if !gateway.Connected() {
		t.Fatal("reopen must restore connected state")
	}
}

func TestGatewayLivenessNoReopenWhenFresh(t *testing.T) {
	gateway, err := NewGateway("token")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	gateway.ConfigureReopenHook(func() error { calls++; return nil })
	gateway.markGatewayEvent(true, nowMillis())
	if gateway.shouldReopen(time.Now()) {
		t.Fatal("fresh event must not trigger reopen")
	}
	gateway.checkGatewayLiveness(time.Now())
	if calls != 0 {
		t.Fatalf("reopen calls = %d, want 0", calls)
	}
}

func TestGatewayLivenessNoReopenBeforeFirstEvent(t *testing.T) {
	gateway, err := NewGateway("token")
	if err != nil {
		t.Fatal(err)
	}
	if gateway.shouldReopen(time.Now()) {
		t.Fatal("never-connected gateway must not trigger watchdog reopen")
	}
}

func TestGatewayLivenessBackoffRetries(t *testing.T) {
	old := gatewayReconnectBaseDelay
	gatewayReconnectBaseDelay = time.Millisecond
	defer func() { gatewayReconnectBaseDelay = old }()
	attempts := 0
	errBoom := errors.New("boom")
	err := reopenWithBackoffCall(func() error {
		attempts++
		if attempts < 3 {
			return errBoom
		}
		return nil
	})
	if err != nil {
		t.Fatalf("backoff must succeed on 3rd attempt: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if err := reopenWithBackoffCall(nil); err != nil {
		t.Fatalf("nil hook must be a no-op success: %v", err)
	}
}

func TestGatewayLivenessCloseMarksOffline(t *testing.T) {
	gateway, err := NewGateway("token")
	if err != nil {
		t.Fatal(err)
	}
	gateway.markGatewayEvent(true, nowMillis())
	if !gateway.Connected() {
		t.Fatal("event must mark gateway connected")
	}
	if err := gateway.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gateway.Connected() {
		t.Fatal("Close must mark gateway offline")
	}
}
