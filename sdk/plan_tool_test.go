package sdk

import (
	"context"
	"strings"
	"testing"
)

type planningTestExecutor struct{ called bool }

func (e *planningTestExecutor) Definitions() []Tool { return []Tool{{Name: "run_command"}} }
func (e *planningTestExecutor) Execute(context.Context, ToolCall) ToolResult {
	e.called = true
	return ToolResult{Content: "executed"}
}

func TestPlanningToolRequiresPlanFirst(t *testing.T) {
	base := &planningTestExecutor{}
	session := NewSession(SessionConfig{ID: "test"}, nil)
	e := newPlanningToolExecutor(base, session)
	blocked := e.Execute(context.Background(), ToolCall{ID: "1", Name: "run_command", Arguments: `{}`})
	if !blocked.IsError || base.called {
		t.Fatal("execution ran before plan")
	}
	planned := e.Execute(context.Background(), ToolCall{ID: "2", Name: "plan", Arguments: `{"plan":"inspect A\nimplement B\nverify C"}`})
	if planned.IsError {
		t.Fatalf("plan failed: %s", planned.Content)
	}
	state := session.Plan()
	if len(state.Steps) != 3 || state.Current != 0 {
		t.Fatalf("unexpected plan: %+v", state)
	}
	delegated := &subAgentStub{}
	e.ConfigureSubAgent(delegated)
	result := e.Execute(context.Background(), ToolCall{ID: "3", Name: "delegate_to_subagent", Arguments: `{"task":"inspect A"}`})
	if result.IsError {
		t.Fatalf("delegation failed: %s", result.Content)
	}
	if !delegated.called {
		t.Fatal("sub-agent was not invoked")
	}
}

type subAgentStub struct{ called bool }

func (s *subAgentStub) Delegate(context.Context, string) (string, error) {
	s.called = true
	return "sa-test", nil
}
func (s *subAgentStub) Status(string) string  { return "status=running" }
func (s *subAgentStub) History(string) string { return "history" }
func (s *subAgentStub) Stop(string) bool      { return true }

func TestPlanningToolDefinition(t *testing.T) {
	e := newPlanningToolExecutor(&planningTestExecutor{}, nil)
	defs := e.Definitions()
	var plan, delegate bool
	for _, d := range defs {
		if d.Name == planningToolName {
			plan = true
		}
		if d.Name == "delegate_to_subagent" {
			delegate = true
		}
	}
	if !plan {
		t.Fatal("plan tool definition missing")
	}
	if delegate {
		t.Fatal("sub-agent tools should not exist before runner configuration")
	}
}

func TestParsePlanSteps(t *testing.T) {
	steps := parsePlanSteps("1. inspect A\n2) implement B\n- verify C")
	if len(steps) != 3 || !strings.EqualFold(steps[0], "inspect A") || !strings.EqualFold(steps[2], "verify C") {
		t.Fatalf("unexpected steps: %#v", steps)
	}
}
