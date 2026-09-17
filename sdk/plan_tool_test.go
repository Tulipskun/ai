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
func (s *subAgentStub) Continue(context.Context, string, string) (string, error) {
	return "continued", nil
}
func (s *subAgentStub) Accept(string, string) error { return nil }
func (s *subAgentStub) Delegate(context.Context, string) (string, error) {
	s.called = true
	return "sa-test", nil
}
func (s *subAgentStub) Status(string) string { return "status=running" }
func (s *subAgentStub) Stop(context.Context, string) (string, error) {
	return "stopped", nil
}

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
		case planningToolName, "delegate_to_subagent", "stop_subagent", "follow_up_subagent", "continue_subagent", "accept_subagent_result":
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
	got := planningSystemPrompt(base, PlanState{})
	if !strings.HasPrefix(got, base+"\n\n") {
		t.Fatalf("context was modified: %s", got)
	}
	for _, want := range []string{"You are the Main Agent", "do not have execution tools", "take precedence over any conflicting direct-execution instructions"} {
		if !strings.Contains(got, want) {
			t.Fatalf("role boundary missing %q: %s", want, got)
		}
	}
}

func TestPlanningPromptInjectsChecklist(t *testing.T) {
	plan := PlanState{Revision: 2, Steps: []PlanStep{
		{Index: 1, Text: "inspect A", Status: "completed"},
		{Index: 2, Text: "implement B", Status: "ready"},
		{Index: 3, Text: "verify C", Status: "pending"},
	}}
	got := planningSystemPrompt("base", plan)
	for _, want := range []string{"[x] 1. inspect A (completed)", "[>] 2. implement B (ready)", "[ ] 3. verify C (pending)", "you own these statuses"} {
		if !strings.Contains(got, want) {
			t.Fatalf("checklist injection missing %q: %s", want, got)
		}
	}
	if strings.Contains(planningSystemPrompt("base", PlanState{}), "Current checklist") {
		t.Fatal("empty plan must not inject a checklist")
	}
}

func TestParsePlanSteps(t *testing.T) {
	steps := parsePlanSteps("1. inspect A\n2) implement B\n- verify C")
	if len(steps) != 3 || !strings.EqualFold(steps[0], "inspect A") || !strings.EqualFold(steps[2], "verify C") {
		t.Fatalf("unexpected steps: %#v", steps)
	}
}

func TestPlanningGuidanceRequiresProportionalEffort(t *testing.T) {
	for _, want := range []string{"Scale effort to the task", "skip the separate investigation", "fewest steps", "minimal sufficient check"} {
		if !strings.Contains(planningSystemInstruction, want) {
			t.Fatalf("planning guidance missing %q", want)
		}
	}
	for _, want := range []string{"minimal sufficient check", "Do not repeat equivalent listings", "do not try another command formulation"} {
		if !strings.Contains(defaultSubAgentSystemPrompt, want) {
			t.Fatalf("worker prompt missing %q", want)
		}
	}
}

func TestPlanningGuidanceShortCircuitsNonTasks(t *testing.T) {
	if !strings.Contains(planningSystemInstruction, "no tool calls and no plan") {
		t.Fatal("planner must answer non-task messages directly without planning")
	}
}

func TestLoopControlDisciplineInPrompts(t *testing.T) {
	for _, want := range []string{"tool budget", "requirements/loop-control.md", "requirements/lessons.md", "REQ-045"} {
		if !strings.Contains(planningSystemInstruction, want) {
			t.Fatalf("planner prompt missing loop-control discipline %q", want)
		}
	}
	for _, want := range []string{"tool budget", "requirements/loop-control.md", "requirements/lessons.md"} {
		if !strings.Contains(defaultSubAgentSystemPrompt, want) {
			t.Fatalf("worker prompt missing loop-control discipline %q", want)
		}
	}
}
