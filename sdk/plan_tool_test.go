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
	blocked = e.Execute(context.Background(), ToolCall{ID: "after-plan", Name: "run_command", Arguments: `{}`})
	if !blocked.IsError || base.called {
		t.Fatal("main executed a worker tool after planning")
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

func (s *subAgentStub) FollowUp(context.Context, string, string) (string, error) {
	return "followup", nil
}
func (s *subAgentStub) Accept(string, string) error { return nil }
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

func TestMainAgentDefinitionsContainNoExecutionTools(t *testing.T) {
	e := newPlanningToolExecutor(&planningTestExecutor{}, nil)
	e.ConfigureSubAgent(&subAgentStub{})
	defs := e.Definitions()
	for _, d := range defs {
		switch d.Name {
		case planningToolName, "delegate_to_subagent", "subagent_status", "subagent_history", "stop_subagent", "follow_up_subagent", "accept_subagent_result":
		default:
			t.Fatalf("Main Agent exposed non-orchestration tool %q", d.Name)
		}
	}
}

func TestPlanningSystemPromptPreservesContext(t *testing.T) {
	base := `Custom instruction: preserve this context.
Available tools:
- run_command: legacy execution description
Project Requirements (repository source of truth):
- search_files must respect workspace boundaries.
Unrelated custom instructions after the requirements must survive.`
	got := planningSystemPrompt(base)
	if !strings.HasPrefix(got, base+"\n\n") {
		t.Fatalf("context was modified: %s", got)
	}
	for _, want := range []string{"You are the Main Agent", "do not have execution tools", "take precedence over any conflicting direct-execution instructions"} {
		if !strings.Contains(got, want) {
			t.Fatalf("role boundary missing %q: %s", want, got)
		}
	}
}

func TestParsePlanSteps(t *testing.T) {
	steps := parsePlanSteps("1. inspect A\n2) implement B\n- verify C")
	if len(steps) != 3 || !strings.EqualFold(steps[0], "inspect A") || !strings.EqualFold(steps[2], "verify C") {
		t.Fatalf("unexpected steps: %#v", steps)
	}
}
