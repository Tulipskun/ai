package discord

import (
	"strings"
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func TestActorTraceFooterShowsTurnAndSession(t *testing.T) {
	turn := sdk.Usage{InputTokens: 120, OutputTokens: 45, TotalTokens: 165, CacheReadTokens: 30, CacheWriteTokens: 5}
	session := sdk.Usage{InputTokens: 800, OutputTokens: 400, TotalTokens: 1200, CacheReadTokens: 100, CacheWriteTokens: 20}
	footer := actorTraceFooter(turn, session, true, nowMillis())
	for _, want := range []string{"turn in:", "session in:", "120/30", "45/5", "800/100", "400/20", "⏱"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer %q missing %q", footer, want)
		}
	}
}

func TestActorTraceFooterSumsTurnWithoutTerminalDoubleCount(t *testing.T) {
	g := &Gateway{}
	resetActorTrace()
	key := "footer-sum-key"
	first := sdk.Usage{InputTokens: 100, OutputTokens: 10, TotalTokens: 110, CacheReadTokens: 20, CacheWriteTokens: 2}
	second := sdk.Usage{InputTokens: 50, OutputTokens: 5, TotalTokens: 55, CacheReadTokens: 7, CacheWriteTokens: 1}
	g.countActorContentUsage(key, first)
	g.countActorTerminalUsage(key, first) // terminal repeat of same call: counted once
	g.disarmActorDedupe(key)              // tool activity separates provider calls
	g.countActorContentUsage(key, second)
	g.countActorTerminalUsage(key, second)
	actorTraceMu.Lock()
	got := actorTraceStates[key].turnUsage
	actorTraceMu.Unlock()
	want := addUsage(first, second)
	if got != want {
		t.Fatalf("turn usage = %+v, want %+v", got, want)
	}
	footer := actorTraceFooter(got, sdk.Usage{}, false, nowMillis())
	for _, want := range []string{"turn in:", "150/27", "15/3"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer %q missing %q", footer, want)
		}
	}
}
