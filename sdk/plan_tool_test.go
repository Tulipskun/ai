package sdk

import (
	"context"
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
	var found bool
	for _, d := range defs {
		if d.Name == planningToolName {
			found = true
			if d.Description == "" || d.InputSchema == nil {
				t.Fatal("planning tool definition is incomplete")
			}
		}
	}
	if !found {
		t.Fatal("planning tool definition is missing")
	}
}
