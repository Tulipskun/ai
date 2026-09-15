package sdk

import (
	"context"
	"reflect"
	"testing"
)

func TestModeSettersWriteActiveSide(t *testing.T) {
	mainKeys := NewKeyPool("m1", "m2")
	subKeys := NewKeyPool("s1")
	session := NewSession(SessionConfig{ID: "modes", Provider: "B.ai", Model: "m1"}, mainKeys)
	if err := session.SetAgentMode(AgentModeSub); err != nil {
		t.Fatal(err)
	}
	if err := session.SetProvider("C.ai", subKeys); err != nil {
		t.Fatal(err)
	}
	if err := session.SetModel("c1"); err != nil {
		t.Fatal(err)
	}
	if err := session.SetTemperature(0.3); err != nil {
		t.Fatal(err)
	}
	if err := session.SetThinkingLevel(ThinkingLow); err != nil {
		t.Fatal(err)
	}
	if err := session.SetKeyIndex(0); err != nil {
		t.Fatal(err)
	}
	got := session.Config()
	if got.Provider != "B.ai" || got.Model != "m1" {
		t.Fatalf("main side must be untouched: %+v", got)
	}
	if got.Sub.Provider != "C.ai" || got.Sub.Model != "c1" || got.Sub.ThinkingLevel != ThinkingLow {
		t.Fatalf("sub side wrong: %+v", got.Sub)
	}
	if got.Sub.Temperature == nil || *got.Sub.Temperature != 0.3 {
		t.Fatalf("sub temp wrong: %+v", got.Sub.Temperature)
	}
	effective := session.EffectiveConfig()
	if effective.Provider != "C.ai" || effective.Model != "c1" || effective.ThinkingLevel != ThinkingLow {
		t.Fatalf("effective must overlay sub: %+v", effective)
	}
	if key, err := session.APIKey(); err != nil || key != "s1" {
		t.Fatalf("sub turn must use the sub pool: %q %v", key, err)
	}
	if err := session.SetAgentMode(AgentModeMain); err != nil {
		t.Fatal(err)
	}
	if key, err := session.APIKey(); err != nil || key != "m1" {
		t.Fatalf("main turn must use the main pool: %q %v", key, err)
	}
	if effective := session.EffectiveConfig(); effective.Provider != "B.ai" {
		t.Fatalf("main effective wrong: %+v", effective)
	}
}

func TestEnsureSubSettingsSeedsOnce(t *testing.T) {
	keys := NewKeyPool("k1")
	session := NewSession(SessionConfig{ID: "seed", Provider: "B.ai", Model: "m1", KeyIndex: 0}, keys)
	if err := session.EnsureSubSettings(keys); err != nil {
		t.Fatal(err)
	}
	sub := session.Config().Sub
	if sub.Provider != "B.ai" || sub.Model != "m1" {
		t.Fatalf("sub must start as a copy of main: %+v", sub)
	}
	other := NewKeyPool("x1")
	if err := session.SetAgentMode(AgentModeSub); err != nil {
		t.Fatal(err)
	}
	if err := session.SetModel("m2"); err != nil {
		t.Fatal(err)
	}
	if err := session.EnsureSubSettings(other); err != nil {
		t.Fatal(err)
	}
	if got := session.Config().Sub.Model; got != "m2" {
		t.Fatalf("seed must not overwrite: %q", got)
	}
	if session.ActiveKeys() != keys {
		t.Fatal("seed must not swap the pool")
	}
}

func TestSetSubSettingsCopiesAndClears(t *testing.T) {
	keys := NewKeyPool("k1")
	subKeys := NewKeyPool("s1", "s2")
	session := NewSession(SessionConfig{ID: "copy", Provider: "B.ai", Model: "m1"}, keys)
	temp := 0.9
	if err := session.SetSubSettings(ModeSettings{Provider: "C.ai", Model: "c1", KeyIndex: 1, ThinkingLevel: ThinkingHigh, Temperature: &temp}, subKeys); err != nil {
		t.Fatal(err)
	}
	got := session.Config().Sub
	if got.Provider != "C.ai" || got.Model != "c1" || got.KeyIndex != 1 || got.ThinkingLevel != ThinkingHigh || got.Temperature == nil || *got.Temperature != 0.9 {
		t.Fatalf("sub copy wrong: %+v", got)
	}
	if session.ActiveKeys() != keys {
		t.Fatal("main pool must be untouched")
	}
	if err := session.SetSubSettings(ModeSettings{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := session.Config().Sub; got != (ModeSettings{}) {
		t.Fatalf("sub must clear: %+v", got)
	}
}

// TestSubTurnUsesSubSettings runs one sub-mode turn through the shared agent
// and asserts the request carries the sub side's provider, model, thinking
// and temperature (REQ-030).
func TestSubTurnUsesSubSettings(t *testing.T) {
	p := &roleTestProvider{agentTestProvider{responses: []Response{
		{Content: []ContentPart{{Type: ContentText, Text: "done"}}},
	}}}
	c, s := newAgentTestSession(p)
	subKeys := NewKeyPool("key")
	if err := s.SetAgentMode(AgentModeSub); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProvider("test", subKeys); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModel("model"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetThinkingLevel(ThinkingHigh); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTemperature(0.5); err != nil {
		t.Fatal(err)
	}
	tools := &agentTestTools{definitions: []Tool{{Name: "run_command", Description: "execute shell", InputSchema: map[string]any{"type": "object"}}}}
	a := &Agent{Client: c, Tools: tools, MaxRetries: -1}
	if _, err := a.RunTurn(context.Background(), s, Turn{Role: RoleUser}, Request{SystemPrompt: "base"}); err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 1 {
		t.Fatalf("requests=%d", len(p.requests))
	}
	req := p.requests[0]
	if req.Provider != "test" || req.Model != "model" {
		t.Fatalf("route wrong: %+v", req)
	}
	if req.ThinkingLevel != ThinkingHigh {
		t.Fatalf("thinking wrong: %+v", req.ThinkingLevel)
	}
	if req.Temperature == nil || *req.Temperature != 0.5 {
		t.Fatalf("temperature wrong: %+v", req.Temperature)
	}
	if !reflect.DeepEqual(req.Tools, tools.Definitions()) {
		t.Fatalf("sub turn must carry full tools: %+v", req.Tools)
	}
}
