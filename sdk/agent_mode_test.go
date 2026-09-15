package sdk

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestSessionAgentModeRoundTrip(t *testing.T) {
	session := NewSession(SessionConfig{ID: "mode"}, NewKeyPool("key"))
	if got := session.Config().AgentMode; got != "" {
		t.Fatalf("default mode = %q", got)
	}
	if err := session.SetAgentMode(AgentModeSub); err != nil {
		t.Fatal(err)
	}
	if got := session.Config().AgentMode; got != AgentModeSub {
		t.Fatalf("mode = %q", got)
	}
	if err := session.SetAgentMode(AgentModeMain); err != nil {
		t.Fatal(err)
	}
	if got := session.Config().AgentMode; got != AgentModeMain {
		t.Fatalf("mode = %q", got)
	}
	if err := session.SetAgentMode("turbo"); err == nil {
		t.Fatal("invalid mode must fail")
	}
}

// TestSessionAgentModeSelectsRole runs the same shared agent against two
// sessions: the default session plans as the Main Agent while the sub-mode
// session answers as a worker with the full execution tool set and no
// planning prompt wrapper (REQ-029).
func TestSessionAgentModeSelectsRole(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{true: "stream", false: "turn"}[stream], func(t *testing.T) {
			p := &roleTestProvider{agentTestProvider{responses: []Response{
				{Content: []ContentPart{{Type: ContentText, Text: "done"}}},
			}}}
			c, s := newAgentTestSession(p)
			if err := s.SetAgentMode(AgentModeSub); err != nil {
				t.Fatal(err)
			}
			// A sub turn runs on the sub side, which starts empty.
			if err := s.SetProvider("test", NewKeyPool("key")); err != nil {
				t.Fatal(err)
			}
			if err := s.SetModel("model"); err != nil {
				t.Fatal(err)
			}
			tools := &agentTestTools{definitions: []Tool{{Name: "run_command", Description: "execute shell", InputSchema: map[string]any{"type": "object"}}}}
			a := &Agent{Client: c, Tools: tools, MaxRetries: -1}
			base := "channel system prompt"
			if _, err := a.RunTurn(context.Background(), s, Turn{Role: RoleUser}, Request{Stream: stream, SystemPrompt: base}); err != nil {
				t.Fatal(err)
			}
			if len(p.requests) != 1 {
				t.Fatalf("requests=%d", len(p.requests))
			}
			req := p.requests[0]
			if req.SystemPrompt != base || strings.Contains(req.SystemPrompt, planningSystemInstruction) {
				t.Fatalf("sub prompt wrapped: %q", req.SystemPrompt)
			}
			if !reflect.DeepEqual(req.Tools, tools.Definitions()) {
				t.Fatalf("sub tools=%+v", req.Tools)
			}
		})
	}
}
