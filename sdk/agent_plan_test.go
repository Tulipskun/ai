package sdk

import "testing"

func TestParseAgentPlanPlainText(t *testing.T) {
	plan := parseAgentPlan("Plan:\n1. Check config\n2) Call provider\n- Return result")
	if len(plan.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(plan.Steps))
	}
	want := []string{"Check config", "Call provider", "Return result"}
	for i, step := range plan.Steps {
		if step.Goal != want[i] {
			t.Fatalf("step %d = %q, want %q", i+1, step.Goal, want[i])
		}
	}
}

func TestParseAgentPlanPlainLines(t *testing.T) {
	plan := parseAgentPlan("Inspect files\nCall provider\nCheck result")
	if len(plan.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(plan.Steps))
	}
	if plan.Steps[0].Goal != "Inspect files" || plan.Steps[2].Goal != "Check result" {
		t.Fatalf("unexpected steps: %#v", plan.Steps)
	}
}
