package sdk

import (
	"context"
	"strings"
	"testing"
)

type planningTestExecutor struct {
	called bool
}

func (e *planningTestExecutor) Definitions() []Tool {
	return []Tool{{Name: "run_command"}}
}

func (e *planningTestExecutor) Execute(context.Context, ToolCall) ToolResult {
	e.called = true
	return ToolResult{Content: "executed"}
}

func TestPlanningToolRequiresPlanFirst(t *testing.T) {
	base := &planningTestExecutor{}
	e := newPlanningToolExecutor(base)

	blocked := e.Execute(context.Background(), ToolCall{ID: "1", Name: "run_command", Arguments: `{}`})
	if !blocked.IsError {
		t.Fatal("expected execution tool to be blocked before planning")
	}
	if base.called {
		t.Fatal("execution tool ran before plan")
	}

	planned := e.Execute(context.Background(), ToolCall{ID: "2", Name: "plan", Arguments: `{"plan":"Inspect the project, make the change, and verify it."}`})
	if planned.IsError {
		t.Fatalf("plan failed: %s", planned.Content)
	}
	if planned.Content == "" || planned.Content == "Execution plan recorded." {
		t.Fatal("plan result should acknowledge the recorded plan")
	}

	result := e.Execute(context.Background(), ToolCall{ID: "3", Name: "run_command", Arguments: `{}`})
	if result.IsError {
		t.Fatalf("execution remained blocked: %s", result.Content)
	}
	if !base.called {
		t.Fatal("execution tool did not run after planning")
	}
}

func TestPlanningToolDefinition(t *testing.T) {
	e := newPlanningToolExecutor(&planningTestExecutor{})
	defs := e.Definitions()
	found := map[string]Tool{}
	for _, d := range defs {
		if d.Name == planningToolName || d.Name == planningCheckToolName {
			found[d.Name] = d
		}
	}
	for _, name := range []string{planningToolName, planningCheckToolName} {
		d, ok := found[name]
		if !ok {
			t.Fatalf("planning tool definition %q is missing", name)
		}
		if d.Description == "" || d.InputSchema == nil {
			t.Fatalf("planning tool definition %q is incomplete", name)
		}
	}
}

func TestPlanningStepsArrayChecklist(t *testing.T) {
	e := newPlanningToolExecutor(&planningTestExecutor{})
	res := e.Execute(context.Background(), ToolCall{ID: "1", Name: "plan", Arguments: `{"steps":["inspect","change","verify"]}`})
	if res.IsError {
		t.Fatalf("plan failed: %s", res.Content)
	}
	if len(e.steps) != 3 {
		t.Fatalf("steps=%d, want 3", len(e.steps))
	}
	if !e.HasIncomplete() {
		t.Fatal("checklist should be incomplete after planning")
	}
	if got := e.DoneCount(); got != 0 {
		t.Fatalf("done=%d, want 0", got)
	}

	check := e.Execute(context.Background(), ToolCall{ID: "2", Name: "plan_check", Arguments: `{"step":2}`})
	if check.IsError {
		t.Fatalf("plan_check failed: %s", check.Content)
	}
	if !e.steps[1].Done || e.steps[0].Done || e.steps[2].Done {
		t.Fatalf("wrong check state: %+v", e.steps)
	}
	if got := e.DoneCount(); got != 1 {
		t.Fatalf("done=%d, want 1", got)
	}
	if !e.HasIncomplete() {
		t.Fatal("checklist should still be incomplete")
	}

	for _, step := range []string{`{"step":1}`, `{"step":3}`} {
		if r := e.Execute(context.Background(), ToolCall{ID: "x", Name: "plan_check", Arguments: step}); r.IsError {
			t.Fatalf("plan_check %s failed: %s", step, r.Content)
		}
	}
	if e.HasIncomplete() {
		t.Fatal("checklist should be complete")
	}
	if e.PendingPrompt() != "" {
		t.Fatalf("pending prompt should be empty when complete, got %q", e.PendingPrompt())
	}
}

func TestPlanCheckByGoalAndReopen(t *testing.T) {
	e := newPlanningToolExecutor(&planningTestExecutor{})
	if r := e.Execute(context.Background(), ToolCall{ID: "1", Name: "plan", Arguments: `{"steps":[{"goal":"migrate database"},{"goal":"deploy service"}]}`}); r.IsError {
		t.Fatalf("plan failed: %s", r.Content)
	}
	if r := e.Execute(context.Background(), ToolCall{ID: "2", Name: "plan_check", Arguments: `{"goal":"migrate"}`}); r.IsError {
		t.Fatalf("goal check failed: %s", r.Content)
	}
	if !e.steps[0].Done {
		t.Fatalf("goal match did not check step: %+v", e.steps)
	}
	reopen := e.Execute(context.Background(), ToolCall{ID: "3", Name: "plan_check", Arguments: `{"step":1,"done":false}`})
	if reopen.IsError {
		t.Fatalf("reopen failed: %s", reopen.Content)
	}
	if e.steps[0].Done {
		t.Fatal("step should be reopened")
	}
	if p := e.PendingPrompt(); !strings.Contains(p, "migrate database") || !strings.Contains(p, "deploy service") {
		t.Fatalf("pending prompt missing steps: %q", p)
	}
	if r := e.Execute(context.Background(), ToolCall{ID: "4", Name: "plan_check", Arguments: `{"step":9}`}); !r.IsError {
		t.Fatal("out-of-range step should fail")
	}
}

func TestLegacyPlanStringSplitsSteps(t *testing.T) {
	e := newPlanningToolExecutor(&planningTestExecutor{})
	res := e.Execute(context.Background(), ToolCall{ID: "1", Name: "plan", Arguments: `{"plan":"- [x] inspect\n- [ ] change\n2. verify"}`})
	if res.IsError {
		t.Fatalf("plan failed: %s", res.Content)
	}
	if len(e.steps) != 3 {
		t.Fatalf("steps=%d, want 3 (%+v)", len(e.steps), e.steps)
	}
	if !e.steps[0].Done || e.steps[1].Done || e.steps[2].Done {
		t.Fatalf("pre-checked state wrong: %+v", e.steps)
	}
	if e.steps[1].Goal != "change" || e.steps[2].Goal != "verify" {
		t.Fatalf("goals not stripped: %+v", e.steps)
	}
	if got := e.Checklist(); !strings.Contains(got, "1. [x] inspect") || !strings.Contains(got, "2. [ ] change") {
		t.Fatalf("checklist render wrong:\n%s", got)
	}
}

func TestPlanCheckRequiresPlan(t *testing.T) {
	e := newPlanningToolExecutor(&planningTestExecutor{})
	if r := e.Execute(context.Background(), ToolCall{ID: "1", Name: "plan_check", Arguments: `{"step":1}`}); !r.IsError {
		t.Fatal("plan_check before plan should fail")
	}
}
