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

func planCreateArgs(goal string, steps ...string) string {
	quoted := make([]string, 0, len(steps))
	for _, s := range steps {
		quoted = append(quoted, `"`+s+`"`)
	}
	return `{"goal":"` + goal + `","steps":[` + strings.Join(quoted, ",") + `]}`
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

	planned := e.Execute(context.Background(), ToolCall{ID: "2", Name: "plan_create", Arguments: planCreateArgs(
		"inspect the project and land the requested change",
		"inspect the repository layout and locate the target files",
		"make the requested change in the target files",
		"run the test suite and verify the change",
	)})
	if planned.IsError {
		t.Fatalf("plan_create failed: %s", planned.Content)
	}
	if e.goal == "" || len(e.steps) != 3 {
		t.Fatalf("plan state wrong: goal=%q steps=%+v", e.goal, e.steps)
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
		switch d.Name {
		case planningToolName, planningCheckToolName, planningUpdateToolName, planningCloseToolName:
			found[d.Name] = d
		}
	}
	for _, name := range []string{planningToolName, planningCheckToolName, planningUpdateToolName, planningCloseToolName} {
		d, ok := found[name]
		if !ok {
			t.Fatalf("planning tool definition %q is missing", name)
		}
		if d.Description == "" || d.InputSchema == nil {
			t.Fatalf("planning tool definition %q is incomplete", name)
		}
	}
	if planningToolName != "plan_create" {
		t.Fatalf("plan tool was not renamed: %q", planningToolName)
	}
}

func TestPlanCreateRejectsVagueSteps(t *testing.T) {
	e := newPlanningToolExecutor(&planningTestExecutor{})

	if r := e.Execute(context.Background(), ToolCall{ID: "1", Name: "plan_create", Arguments: `{"steps":["inspect the repository layout first"]}`}); !r.IsError {
		t.Fatal("missing goal should fail")
	} else if !strings.Contains(r.Content, "goal") {
		t.Fatalf("error should mention goal: %q", r.Content)
	}

	if r := e.Execute(context.Background(), ToolCall{ID: "2", Name: "plan_create", Arguments: `{"goal":"ship the fix","steps":[]}`}); !r.IsError {
		t.Fatal("empty steps should fail")
	}

	if r := e.Execute(context.Background(), ToolCall{ID: "3", Name: "plan_create", Arguments: planCreateArgs("ship the fix", "fix it")}); !r.IsError {
		t.Fatal("generic step should fail")
	} else if !strings.Contains(r.Content, "generic") {
		t.Fatalf("error should say generic: %q", r.Content)
	}

	if r := e.Execute(context.Background(), ToolCall{ID: "4", Name: "plan_create", Arguments: planCreateArgs("ship the fix", "fix")}); !r.IsError {
		t.Fatal("short step should fail")
	} else if !strings.Contains(r.Content, "vague") {
		t.Fatalf("error should say vague: %q", r.Content)
	}

	dup := "rerun the full test suite until everything passes"
	if r := e.Execute(context.Background(), ToolCall{ID: "5", Name: "plan_create", Arguments: planCreateArgs("ship the fix", dup, dup)}); !r.IsError {
		t.Fatal("duplicate steps should fail")
	}

	if e.planned {
		t.Fatal("failed creates must not mark the executor planned")
	}
}

func TestPlanningStepsArrayChecklist(t *testing.T) {
	e := newPlanningToolExecutor(&planningTestExecutor{})
	res := e.Execute(context.Background(), ToolCall{ID: "1", Name: "plan_create", Arguments: planCreateArgs(
		"land the three ordered changes",
		"inspect the target files and note the required edits",
		"apply the edits to the target files carefully",
		"run the test suite and verify everything passes",
	)})
	if res.IsError {
		t.Fatalf("plan_create failed: %s", res.Content)
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
	if r := e.Execute(context.Background(), ToolCall{ID: "1", Name: "plan_create", Arguments: `{"goal":"migrate then deploy","steps":[{"goal":"migrate the production database schema"},{"goal":"deploy the service to production"}]}`}); r.IsError {
		t.Fatalf("plan_create failed: %s", r.Content)
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
	if p := e.PendingPrompt(); !strings.Contains(p, "migrate the production database schema") || !strings.Contains(p, "deploy the service to production") {
		t.Fatalf("pending prompt missing steps: %q", p)
	}
	if r := e.Execute(context.Background(), ToolCall{ID: "4", Name: "plan_check", Arguments: `{"step":9}`}); !r.IsError {
		t.Fatal("out-of-range step should fail")
	}
}

func TestPlanCheckRequiresPlan(t *testing.T) {
	e := newPlanningToolExecutor(&planningTestExecutor{})
	if r := e.Execute(context.Background(), ToolCall{ID: "1", Name: "plan_check", Arguments: `{"step":1}`}); !r.IsError {
		t.Fatal("plan_check before plan should fail")
	}
	if r := e.Execute(context.Background(), ToolCall{ID: "2", Name: "plan_update", Arguments: `{"steps":["re-read the failing file and adjust"],"reason":"need a plan first"}`}); !r.IsError {
		t.Fatal("plan_update before plan_create should fail")
	}
}

func TestPlanUpdateRevisesStepsAndLocksGoal(t *testing.T) {
	e := newPlanningToolExecutor(&planningTestExecutor{})
	if r := e.Execute(context.Background(), ToolCall{ID: "1", Name: "plan_create", Arguments: planCreateArgs(
		"fix the login timeout bug",
		"read the session handler source to find the timeout",
		"reproduce the timeout with a failing test case",
	)}); r.IsError {
		t.Fatalf("plan_create failed: %s", r.Content)
	}
	if r := e.Execute(context.Background(), ToolCall{ID: "2", Name: "plan_check", Arguments: `{"step":1}`}); r.IsError {
		t.Fatalf("plan_check failed: %s", r.Content)
	}

	updated := e.Execute(context.Background(), ToolCall{ID: "3", Name: "plan_update", Arguments: `{"steps":["read the session handler source to find the timeout","add retry with backoff around the session refresh call","reproduce the timeout with a failing test case"],"reason":"the handler delegates to refresh logic, so the retry belongs there"}`})
	if updated.IsError {
		t.Fatalf("plan_update failed: %s", updated.Content)
	}
	if len(e.steps) != 3 {
		t.Fatalf("steps=%d, want 3", len(e.steps))
	}
	if !e.steps[0].Done {
		t.Fatalf("unchanged done step should stay checked: %+v", e.steps)
	}
	if e.steps[1].Done || e.steps[2].Done {
		t.Fatalf("new and pending steps must stay open: %+v", e.steps)
	}
	if e.goal != "fix the login timeout bug" {
		t.Fatalf("goal moved: %q", e.goal)
	}

	drift := e.Execute(context.Background(), ToolCall{ID: "4", Name: "plan_update", Arguments: `{"goal":"rewrite the billing module","steps":["rewrite the billing module from scratch now"],"reason":"found a bigger problem"}`})
	if !drift.IsError {
		t.Fatal("goal change should be rejected")
	}
	if e.goal != "fix the login timeout bug" {
		t.Fatalf("goal moved after rejected update: %q", e.goal)
	}

	if r := e.Execute(context.Background(), ToolCall{ID: "5", Name: "plan_update", Arguments: `{"steps":["read the config file again for the timeout value"],"reason":""}`}); !r.IsError {
		t.Fatal("missing reason should fail")
	}
	if r := e.Execute(context.Background(), ToolCall{ID: "6", Name: "plan_update", Arguments: `{"steps":["fix it"],"reason":"the file shows a different root cause here"}`}); !r.IsError {
		t.Fatal("vague replacement steps should fail")
	}
}

func TestPlanCloseRequiresReasonAndPlan(t *testing.T) {
	e := newPlanningToolExecutor(&planningTestExecutor{})
	if r := e.Execute(context.Background(), ToolCall{ID: "0", Name: "plan_close", Arguments: `{"reason":"blocked by missing permission on the host"}`}); !r.IsError {
		t.Fatal("plan_close before plan_create should fail")
	}
	if r := e.Execute(context.Background(), ToolCall{ID: "1", Name: "plan_create", Arguments: planCreateArgs(
		"restart the stopped service",
		"inspect the stopped service logs for errors",
		"restart the service and verify it responds",
	)}); r.IsError {
		t.Fatalf("plan_create failed: %s", r.Content)
	}
	if r := e.Execute(context.Background(), ToolCall{ID: "2", Name: "plan_close", Arguments: `{"reason":""}`}); !r.IsError {
		t.Fatal("empty reason should fail")
	}
	if r := e.Execute(context.Background(), ToolCall{ID: "3", Name: "plan_close", Arguments: `{"reason":"no"}`}); !r.IsError {
		t.Fatal("short reason should fail")
	}
	if e.IsClosed() {
		t.Fatal("failed closes must not close the plan")
	}
}

func TestPlanCloseBlocksToolsButAllowsFinish(t *testing.T) {
	base := &planningTestExecutor{}
	e := newPlanningToolExecutor(base)
	if r := e.Execute(context.Background(), ToolCall{ID: "1", Name: "plan_create", Arguments: planCreateArgs(
		"restart the stopped service",
		"inspect the stopped service logs for errors",
		"restart the service and verify it responds",
	)}); r.IsError {
		t.Fatalf("plan_create failed: %s", r.Content)
	}
	if r := e.Execute(context.Background(), ToolCall{ID: "2", Name: "plan_check", Arguments: `{"step":1}`}); r.IsError {
		t.Fatalf("plan_check failed: %s", r.Content)
	}
	closed := e.Execute(context.Background(), ToolCall{ID: "3", Name: "plan_close", Arguments: `{"reason":"the service host is unreachable from this environment","next_steps":"retry when the host is reachable"}`})
	if closed.IsError {
		t.Fatalf("plan_close failed: %s", closed.Content)
	}
	if !e.IsClosed() {
		t.Fatal("plan should be closed")
	}
	for _, want := range []string{"1/2 steps done", "unreachable", "retry when the host is reachable", "final answer"} {
		if !strings.Contains(closed.Content, want) {
			t.Fatalf("close report missing %q: %q", want, closed.Content)
		}
	}
	if e.PendingPrompt() != "" {
		t.Fatalf("closed plan should not nudge: %q", e.PendingPrompt())
	}
	if r := e.Execute(context.Background(), ToolCall{ID: "4", Name: "run_command", Arguments: `{}`}); !r.IsError {
		t.Fatal("execution tools should be blocked after close")
	}
	if base.called {
		t.Fatal("execution tool ran after close")
	}
	if r := e.Execute(context.Background(), ToolCall{ID: "5", Name: "plan_check", Arguments: `{"step":2}`}); !r.IsError {
		t.Fatal("plan_check should be rejected after close")
	}
	if r := e.Execute(context.Background(), ToolCall{ID: "6", Name: "plan_update", Arguments: `{"steps":["restart the service and verify it responds now"],"reason":"host is back online today"}`}); !r.IsError {
		t.Fatal("plan_update should be rejected after close")
	}
	if r := e.Execute(context.Background(), ToolCall{ID: "7", Name: "plan_close", Arguments: `{"reason":"closing an already closed plan here"}`}); !r.IsError {
		t.Fatal("double close should fail")
	}
}
