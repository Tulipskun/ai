package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const planningToolName = "plan"

const planningCheckToolName = "plan_check"

// maxPlanIncompleteNudges caps how many times the agent is reminded to finish
// an incomplete checklist before it is allowed to return a final answer.
// The cap prevents an unbounded tool loop when the model refuses to check steps.
const maxPlanIncompleteNudges = 5

const planningToolDescription = "Create the execution plan for the user's goal before using any execution tool. Provide the work as an ordered checklist in `steps` (array of goal strings or {goal} objects). This is a planning action, not the final answer."

const planningCheckToolDescription = "Mark a plan checklist step done (or reopen it) when its work finishes. Call it once per completed step, then continue until every step is checked before giving the final answer."

const planningSystemInstruction = "For tasks that require work with tools, call the `plan` tool first with the work as a `steps` checklist. Use the returned plan as the basis for execution, then call the tools needed to complete it. Call `plan_check` with the 1-based step number as each step finishes. Do not give the final answer until every checklist step is checked done. Do not expose tool-call arguments or internal planning format as the user-facing answer."

// PlanStep is a single checklist entry tracked by the planning executor.
type PlanStep struct {
	Goal string
	Done bool
}

type planStepInput struct {
	Goal string
}

// UnmarshalJSON accepts either a plain goal string or an object carrying the
// goal under goal/description/text/title keys.
func (s *planStepInput) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		s.Goal = strings.TrimSpace(str)
		return nil
	}
	var obj struct {
		Goal        string `json:"goal"`
		Description string `json:"description"`
		Text        string `json:"text"`
		Title       string `json:"title"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	for _, v := range []string{obj.Goal, obj.Description, obj.Text, obj.Title} {
		if strings.TrimSpace(v) != "" {
			s.Goal = strings.TrimSpace(v)
			return nil
		}
	}
	return fmt.Errorf("plan step needs a goal string")
}

type planningToolInput struct {
	Plan  string           `json:"plan"`
	Steps []planStepInput  `json:"steps"`
}

type planCheckInput struct {
	Step   *int   `json:"step"`
	Goal   string `json:"goal"`
	Done   *bool  `json:"done"`
	Status string `json:"status"`
}

type planningToolExecutor struct {
	base    ToolExecutor
	planned bool
	steps   []PlanStep
}

func newPlanningToolExecutor(base ToolExecutor) *planningToolExecutor {
	if base == nil {
		return nil
	}
	return &planningToolExecutor{base: base}
}

func (e *planningToolExecutor) Definitions() []Tool {
	if e == nil || e.base == nil {
		return nil
	}
	defs := append([]Tool(nil), e.base.Definitions()...)
	var hasPlan, hasCheck bool
	for _, d := range defs {
		switch d.Name {
		case planningToolName:
			hasPlan = true
		case planningCheckToolName:
			hasCheck = true
		}
	}
	if !hasPlan {
		defs = append(defs, Tool{
			Name:        planningToolName,
			Description: planningToolDescription,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"steps": map[string]any{
						"type":        "array",
						"description": "Ordered checklist goals. Each entry is a goal string or a {goal} object.",
						"items":       map[string]any{},
					},
					"plan": map[string]any{"type": "string", "description": "Legacy free-text plan; each non-empty line becomes one checklist step."},
				},
			},
		})
	}
	if !hasCheck {
		defs = append(defs, Tool{
			Name:        planningCheckToolName,
			Description: planningCheckToolDescription,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"step":   map[string]any{"type": "integer", "description": "1-based checklist step number to update."},
					"goal":   map[string]any{"type": "string", "description": "Alternative to step: substring matching the step goal."},
					"done":   map[string]any{"type": "boolean", "description": "True marks the step done (default); false reopens it."},
					"status": map[string]any{"type": "string", "description": "Alternative to done: done/completed or pending/reopen."},
				},
			},
		})
	}
	return defs
}

func planningSystemPrompt(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return planningSystemInstruction
	}
	return base + "\n\n" + planningSystemInstruction
}

// systemPromptExtra reports the live checklist state so every provider request
// carries the current progress alongside the static planning instruction.
func (e *planningToolExecutor) systemPromptExtra() string {
	if e == nil || !e.planned || len(e.steps) == 0 {
		return ""
	}
	return "\n\nPlan checklist (call `plan_check` as each step finishes; do not finish until every step is [x]):\n" + e.Checklist()
}

// Checklist renders the steps as 1-based [x]/[ ] lines.
func (e *planningToolExecutor) Checklist() string {
	if e == nil || len(e.steps) == 0 {
		return ""
	}
	var b strings.Builder
	for i, s := range e.steps {
		mark := " "
		if s.Done {
			mark = "x"
		}
		fmt.Fprintf(&b, "%d. [%s] %s", i+1, mark, s.Goal)
		if i+1 < len(e.steps) {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// HasIncomplete reports whether any checklist step is still unchecked.
func (e *planningToolExecutor) HasIncomplete() bool {
	if e == nil || !e.planned {
		return false
	}
	for _, s := range e.steps {
		if !s.Done {
			return true
		}
	}
	return false
}

// DoneCount returns how many steps are checked done.
func (e *planningToolExecutor) DoneCount() int {
	if e == nil {
		return 0
	}
	n := 0
	for _, s := range e.steps {
		if s.Done {
			n++
		}
	}
	return n
}

// PendingPrompt builds the injected reminder listing every unchecked step.
// It is appended to the session when the model tries to finish early.
func (e *planningToolExecutor) PendingPrompt() string {
	if e == nil {
		return ""
	}
	var pending []string
	for i, s := range e.steps {
		if !s.Done {
			pending = append(pending, fmt.Sprintf("(%d) %s", i+1, s.Goal))
		}
	}
	if len(pending) == 0 {
		return ""
	}
	return fmt.Sprintf("Plan incomplete: %d/%d steps done. Still pending:\n- %s\nContinue working the pending steps and call `plan_check` for each one as it finishes. Do not give the final answer yet.",
		e.DoneCount(), len(e.steps), strings.Join(pending, "\n- "))
}

// planReminderTurn wraps the pending prompt as a user turn so the next
// provider request must address the remaining checklist.
func (e *planningToolExecutor) planReminderTurn() Turn {
	return Turn{Role: RoleUser, Content: []ContentPart{{Type: ContentText, Text: e.PendingPrompt()}}}
}

func (e *planningToolExecutor) Execute(ctx context.Context, call ToolCall) ToolResult {
	if e == nil || e.base == nil {
		return ToolResult{ID: call.ID, Content: "tool execution is not configured", IsError: true}
	}
	switch call.Name {
	case planningToolName:
		return e.executePlan(call)
	case planningCheckToolName:
		return e.executeCheck(call)
	}
	if !e.planned {
		return ToolResult{ID: call.ID, Content: "call the `plan` tool before using execution tools", IsError: true}
	}
	return e.base.Execute(ctx, call)
}

func (e *planningToolExecutor) executePlan(call ToolCall) ToolResult {
	var input planningToolInput
	if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
		return ToolResult{ID: call.ID, Content: "invalid plan tool arguments: need `steps` array or `plan` text", IsError: true}
	}
	steps := make([]PlanStep, 0, len(input.Steps)+1)
	for _, s := range input.Steps {
		if s.Goal == "" {
			continue
		}
		steps = append(steps, PlanStep{Goal: s.Goal})
	}
	if len(steps) == 0 {
		steps = splitPlanLines(input.Plan)
	}
	if len(steps) == 0 {
		return ToolResult{ID: call.ID, Content: "plan requires at least one step: pass `steps` or non-empty `plan` text", IsError: true}
	}
	e.steps = steps
	e.planned = true
	return ToolResult{ID: call.ID, Content: fmt.Sprintf("Execution plan recorded (%d steps):\n%s\nCall `plan_check` with each step number as it finishes.", len(e.steps), e.Checklist())}
}

func (e *planningToolExecutor) executeCheck(call ToolCall) ToolResult {
	if !e.planned || len(e.steps) == 0 {
		return ToolResult{ID: call.ID, Content: "call the `plan` tool before `plan_check`", IsError: true}
	}
	var input planCheckInput
	if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
		return ToolResult{ID: call.ID, Content: "invalid plan_check arguments: need `step` number or `goal` text", IsError: true}
	}
	idx, err := e.resolveCheckTarget(input)
	if err != nil {
		return ToolResult{ID: call.ID, Content: err.Error(), IsError: true}
	}
	done := true
	if input.Done != nil {
		done = *input.Done
	} else if s := strings.ToLower(strings.TrimSpace(input.Status)); s != "" {
		switch s {
		case "done", "completed", "complete", "checked", "true", "x":
			done = true
		case "pending", "open", "reopen", "todo", "false":
			done = false
		default:
			return ToolResult{ID: call.ID, Content: fmt.Sprintf("unknown plan_check status %q: use done/completed or pending/reopen", input.Status), IsError: true}
		}
	}
	e.steps[idx].Done = done
	verb := "checked"
	if !done {
		verb = "reopened"
	}
	out := fmt.Sprintf("Step %d %s (%d/%d done):\n%s", idx+1, verb, e.DoneCount(), len(e.steps), e.Checklist())
	if e.HasIncomplete() {
		out += "\n" + e.PendingPrompt()
	}
	return ToolResult{ID: call.ID, Content: out}
}

func (e *planningToolExecutor) resolveCheckTarget(input planCheckInput) (int, error) {
	if input.Step != nil {
		if *input.Step < 1 || *input.Step > len(e.steps) {
			return 0, fmt.Errorf("plan_check step %d out of range (1-%d)", *input.Step, len(e.steps))
		}
		return *input.Step - 1, nil
	}
	goal := strings.TrimSpace(input.Goal)
	if goal != "" {
		lower := strings.ToLower(goal)
		for i, s := range e.steps {
			if strings.Contains(strings.ToLower(s.Goal), lower) {
				return i, nil
			}
		}
		return 0, fmt.Errorf("plan_check goal %q matches no step (1-%d)", input.Goal, len(e.steps))
	}
	return 0, fmt.Errorf("plan_check needs a `step` number (1-%d) or `goal` text", len(e.steps))
}

// splitPlanLines parses legacy free-text plans: each non-empty line becomes one
// step, with markdown bullets, ordered prefixes, and [ ]/[x] markers stripped.
// A pre-checked [x] line starts Done.
func splitPlanLines(plan string) []PlanStep {
	var steps []PlanStep
	for _, line := range strings.Split(plan, "\n") {
		goal, done := parsePlanLine(line)
		if goal == "" {
			continue
		}
		steps = append(steps, PlanStep{Goal: goal, Done: done})
	}
	return steps
}

func parsePlanLine(line string) (string, bool) {
	s := strings.TrimSpace(line)
	if s == "" {
		return "", false
	}
	done := false
	if strings.HasPrefix(s, "- [") || strings.HasPrefix(s, "* [") {
		rest := s[3:]
		if strings.HasPrefix(strings.ToLower(rest), "x]") {
			done = true
		}
		if idx := strings.Index(rest, "]"); idx >= 0 {
			s = strings.TrimSpace(rest[idx+1:])
		}
	}
	s = strings.TrimSpace(strings.TrimLeft(s, "-*•> "))
	s = stripOrderedPrefix(s)
	s = strings.TrimSpace(s)
	return s, done
}

func stripOrderedPrefix(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 0 && i < len(s) && (s[i] == '.' || s[i] == ')') {
		if n, err := strconv.Atoi(s[:i]); err == nil && n > 0 {
			return strings.TrimSpace(s[i+1:])
		}
	}
	return s
}
