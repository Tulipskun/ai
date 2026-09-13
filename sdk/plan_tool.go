package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const planningToolName = "plan_create"

const planningCheckToolName = "plan_check"

const planningUpdateToolName = "plan_update"

const planningCloseToolName = "plan_close"

// maxPlanIncompleteNudges caps how many times the agent is reminded to finish
// an incomplete checklist before it is allowed to return a final answer.
// The cap prevents an unbounded tool loop when the model refuses to check steps.
const maxPlanIncompleteNudges = 5

// Concreteness and scope limits enforced on every plan write.
const (
	minGoalRunes   = 6
	minStepRunes   = 12
	minReasonRunes = 8
	maxPlanSteps   = 15
)

const planningToolDescription = "Create the execution plan for the user's goal before using any execution tool. Pass `goal` (the original objective, locked as scope) plus `steps`: an ordered checklist of concrete actions, each naming what to do, where (file/tool), and the expected outcome. Vague steps are rejected. This is a planning action, not the final answer."

const planningCheckToolDescription = "Mark a plan checklist step done (or reopen it) when its work finishes. Call it once per completed step, then continue until every step is checked before giving the final answer."

const planningUpdateToolDescription = "Revise the plan checklist after new information (file reads, tool results, errors). Pass the full replacement `steps` plus `reason` explaining why the change is needed. The original `goal` is locked: steps must still serve it, and any attempt to change it is rejected."

const planningCloseToolDescription = "Close the plan when the remaining work cannot be completed (system limitation, blocked dependency, or any other reason). Pass `reason` explaining why plus optional `next_steps` for the user. Closing permits the final answer even with unchecked steps; execution tools are blocked afterwards, so call it only when stopping."

const planningSystemInstruction = "For tasks that require work with tools, call the `plan_create` tool first with the original `goal` and a `steps` checklist of concrete actions (each step names what to do, where, and the expected outcome - vague steps are rejected and single generic steps are not accepted). Use the returned plan as the basis for execution, then call the tools needed to complete it. After learning new information (file contents, errors, unexpected results), you may revise the checklist with `plan_update`, giving a `reason`; the revision must still serve the original goal and must never change it. Call `plan_check` with the 1-based step number as each step finishes. Do not give the final answer until every checklist step is checked done. If the remaining work cannot be completed for reasons outside your control (missing permission, unavailable system, blocked dependency, or any other blocker), call `plan_close` with a `reason` and report to the user in the final answer what was completed, what could not be finished and why, plus suggested next steps; after closing, no more execution tools may be called. Do not expose tool-call arguments or internal planning format as the user-facing answer."

// vagueStepPhrases are normalized step texts rejected as non-concrete.
// A concrete step must say what to do, where, and to what end.
var vagueStepPhrases = map[string]bool{
	"fix it": true, "fix the bug": true, "fix bug": true, "fix bugs": true,
	"do it": true, "do the task": true, "complete the task": true, "finish the task": true,
	"implement it": true, "implement the feature": true, "handle it": true,
	"solve the problem": true, "solve it": true, "make it work": true,
	"update the code": true, "write the code": true, "change the code": true,
	"test it": true, "verify it": true, "check it": true,
	"done": true, "finish": true, "complete it": true,
	"แก้บั๊ก": true, "แก้ไข": true, "ทำ": true, "ทำให้เสร็จ": true,
	"จัดการ": true, "ตรวจสอบ": true, "ทดสอบ": true, "เสร็จ": true,
	"ทำตาม": true, "ดำเนินการ": true,
}

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

type planCreateInput struct {
	Goal  string          `json:"goal"`
	Plan  string          `json:"plan"`
	Steps []planStepInput `json:"steps"`
}

type planUpdateInput struct {
	Goal   string          `json:"goal"`
	Steps  []planStepInput `json:"steps"`
	Reason string          `json:"reason"`
}

type planCheckInput struct {
	Step   *int   `json:"step"`
	Goal   string `json:"goal"`
	Done   *bool  `json:"done"`
	Status string `json:"status"`
}

type planCloseInput struct {
	Reason    string `json:"reason"`
	NextSteps string `json:"next_steps"`
}

type planningToolExecutor struct {
	base        ToolExecutor
	planned     bool
	goal        string
	steps       []PlanStep
	closed      bool
	closeReason string
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
	var hasCreate, hasCheck, hasUpdate, hasClose bool
	for _, d := range defs {
		switch d.Name {
		case planningToolName:
			hasCreate = true
		case planningCheckToolName:
			hasCheck = true
		case planningUpdateToolName:
			hasUpdate = true
		case planningCloseToolName:
			hasClose = true
		}
	}
	if !hasCreate {
		defs = append(defs, Tool{
			Name:        planningToolName,
			Description: planningToolDescription,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"goal":  map[string]any{"type": "string", "description": "Original objective; locked as the plan scope and cannot be changed later."},
					"steps": map[string]any{
						"type":        "array",
						"description": "Ordered checklist of concrete actions. Each entry is a goal string or a {goal} object naming what to do, where, and the expected outcome.",
						"items":       map[string]any{},
					},
					"plan": map[string]any{"type": "string", "description": "Fallback free-text steps; each non-empty line becomes one checklist step. `goal` is still required."},
				},
				"required": []string{"goal", "steps"},
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
	if !hasUpdate {
		defs = append(defs, Tool{
			Name:        planningUpdateToolName,
			Description: planningUpdateToolDescription,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"steps": map[string]any{
						"type":        "array",
						"description": "Full replacement checklist; same concreteness rules as plan_create. Done state is kept for steps with unchanged goals.",
						"items":       map[string]any{},
					},
					"reason": map[string]any{"type": "string", "description": "Why the revision is needed (e.g. file contents found, error encountered)."},
					"goal":   map[string]any{"type": "string", "description": "Must be empty or repeat the original goal; changing it is rejected."},
				},
				"required": []string{"steps", "reason"},
			},
		})
	}
	if !hasClose {
		defs = append(defs, Tool{
			Name:        planningCloseToolName,
			Description: planningCloseToolDescription,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason":     map[string]any{"type": "string", "description": "Why the remaining work cannot be completed (limitation, blocker, or any other reason)."},
					"next_steps": map[string]any{"type": "string", "description": "Optional suggested follow-ups for the user."},
				},
				"required": []string{"reason"},
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

// systemPromptExtra reports the locked goal plus live checklist state so every
// provider request carries the scope and current progress alongside the static
// planning instruction. A closed plan reports its closure instead so the model
// writes the final user-facing report rather than continuing execution.
func (e *planningToolExecutor) systemPromptExtra() string {
	if e == nil || !e.planned || len(e.steps) == 0 {
		return ""
	}
	if e.closed {
		return "\n\nPlan CLOSED for goal " + strconv.Quote(e.goal) + " (reason: " + e.closeReason + ").\n" +
			"Checked progress:\n" + e.Checklist() +
			"\nDo not call any more execution tools. Give the final answer now: report what was completed, what could not be finished and why, plus suggested next steps."
	}
	return "\n\nOriginal goal (locked scope - `plan_update` may revise steps but must serve this goal, never change it):\n" +
		e.goal +
		"\n\nPlan checklist (call `plan_check` as each step finishes; do not finish until every step is [x], or close with `plan_close` and a reason if the rest cannot be done):\n" + e.Checklist()
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

// IsClosed reports whether the plan was closed via `plan_close`.
// A closed plan permits the final answer even with unchecked steps.
func (e *planningToolExecutor) IsClosed() bool {
	return e != nil && e.planned && e.closed
}

// PendingPrompt builds the injected reminder listing every unchecked step.
// It is appended to the session when the model tries to finish early.
// A closed plan needs no reminder: the model already committed to reporting.
func (e *planningToolExecutor) PendingPrompt() string {
	if e == nil || e.closed {
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
	return fmt.Sprintf("Plan incomplete for goal %q: %d/%d steps done. Still pending:\n- %s\nContinue working the pending steps and call `plan_check` for each one as it finishes. Do not give the final answer yet.",
		e.goal, e.DoneCount(), len(e.steps), strings.Join(pending, "\n- "))
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
		return e.executeCreate(call)
	case planningUpdateToolName:
		return e.executeUpdate(call)
	case planningCheckToolName:
		return e.executeCheck(call)
	case planningCloseToolName:
		return e.executeClose(call)
	}
	if !e.planned {
		return ToolResult{ID: call.ID, Content: "call the `plan_create` tool before using execution tools", IsError: true}
	}
	if e.closed {
		return ToolResult{ID: call.ID, Content: fmt.Sprintf("plan is closed for goal %q (reason: %s): no more execution tools may be called. Give the final answer reporting what was completed, what could not be finished and why.", e.goal, e.closeReason), IsError: true}
	}
	return e.base.Execute(ctx, call)
}

func (e *planningToolExecutor) executeCreate(call ToolCall) ToolResult {
	var input planCreateInput
	if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
		return ToolResult{ID: call.ID, Content: "invalid plan_create arguments: need `goal` plus `steps` array", IsError: true}
	}
	goal := strings.TrimSpace(input.Goal)
	if len([]rune(goal)) < minGoalRunes {
		return ToolResult{ID: call.ID, Content: fmt.Sprintf("plan_create needs a `goal` of at least %d characters stating the original objective", minGoalRunes), IsError: true}
	}
	steps, err := validatedSteps(input.Steps, splitPlanLines(input.Plan))
	if err != nil {
		return ToolResult{ID: call.ID, Content: "invalid plan_create steps: " + err.Error(), IsError: true}
	}
	e.goal = goal
	e.steps = steps
	e.planned = true
	e.closed = false
	e.closeReason = ""
	return ToolResult{ID: call.ID, Content: fmt.Sprintf("Execution plan recorded for goal %q (%d steps):\n%s\nCall `plan_check` with each step number as it finishes. If new information requires different steps, use `plan_update` with a reason - the goal stays locked. If the rest cannot be done, close with `plan_close` and a reason instead of stalling.", e.goal, len(e.steps), e.Checklist())}
}

func (e *planningToolExecutor) executeClose(call ToolCall) ToolResult {
	if !e.planned || len(e.steps) == 0 {
		return ToolResult{ID: call.ID, Content: "call the `plan_create` tool before `plan_close`", IsError: true}
	}
	if e.closed {
		return ToolResult{ID: call.ID, Content: fmt.Sprintf("plan is already closed for goal %q (reason: %s). Give the final answer now.", e.goal, e.closeReason), IsError: true}
	}
	var input planCloseInput
	if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
		return ToolResult{ID: call.ID, Content: "invalid plan_close arguments: need a `reason` explaining why the remaining work cannot be completed", IsError: true}
	}
	reason := strings.TrimSpace(input.Reason)
	if len([]rune(reason)) < minReasonRunes {
		return ToolResult{ID: call.ID, Content: fmt.Sprintf("plan_close needs a `reason` of at least %d characters explaining why the remaining work cannot be completed", minReasonRunes), IsError: true}
	}
	e.closed = true
	e.closeReason = reason
	var unfinished []string
	for i, s := range e.steps {
		if !s.Done {
			unfinished = append(unfinished, fmt.Sprintf("(%d) %s", i+1, s.Goal))
		}
	}
	out := fmt.Sprintf("Plan closed for goal %q: %d/%d steps done.", e.goal, e.DoneCount(), len(e.steps))
	if len(unfinished) > 0 {
		out += fmt.Sprintf(" Unfinished:\n- %s", strings.Join(unfinished, "\n- "))
	}
	out += fmt.Sprintf("\nReason: %s", e.closeReason)
	if next := strings.TrimSpace(input.NextSteps); next != "" {
		out += fmt.Sprintf("\nSuggested next steps: %s", next)
	}
	out += "\nGive the final answer now: report what was completed, what could not be finished and why, plus suggested next steps. Do not call any more execution tools."
	return ToolResult{ID: call.ID, Content: out}
}

func (e *planningToolExecutor) executeUpdate(call ToolCall) ToolResult {
	if !e.planned || len(e.steps) == 0 {
		return ToolResult{ID: call.ID, Content: "call the `plan_create` tool before `plan_update`", IsError: true}
	}
	if e.closed {
		return ToolResult{ID: call.ID, Content: fmt.Sprintf("plan is closed for goal %q and cannot be updated. Give the final answer now.", e.goal), IsError: true}
	}
	var input planUpdateInput
	if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
		return ToolResult{ID: call.ID, Content: "invalid plan_update arguments: need `steps` array plus `reason`", IsError: true}
	}
	if g := strings.TrimSpace(input.Goal); g != "" && normalizePlanText(g) != normalizePlanText(e.goal) {
		return ToolResult{ID: call.ID, Content: fmt.Sprintf("plan_update rejected: goal is locked to %q and cannot be changed. Revise `steps` to serve it instead.", e.goal), IsError: true}
	}
	if len([]rune(strings.TrimSpace(input.Reason))) < minReasonRunes {
		return ToolResult{ID: call.ID, Content: fmt.Sprintf("plan_update needs a `reason` of at least %d characters explaining what new information requires the change", minReasonRunes), IsError: true}
	}
	steps, err := validatedSteps(input.Steps, nil)
	if err != nil {
		return ToolResult{ID: call.ID, Content: "invalid plan_update steps: " + err.Error(), IsError: true}
	}
	// Keep Done state for steps whose goals survive the revision unchanged.
	kept := map[string]bool{}
	for _, s := range e.steps {
		if s.Done {
			kept[normalizePlanText(s.Goal)] = true
		}
	}
	for i, s := range steps {
		if kept[normalizePlanText(s.Goal)] {
			steps[i].Done = true
		}
	}
	e.steps = steps
	out := fmt.Sprintf("Plan updated for locked goal %q (reason: %s) - %d/%d steps done:\n%s", e.goal, strings.TrimSpace(input.Reason), e.DoneCount(), len(e.steps), e.Checklist())
	if e.HasIncomplete() {
		out += "\n" + e.PendingPrompt()
	}
	return ToolResult{ID: call.ID, Content: out}
}

// validatedSteps converts raw step inputs (or fallback free-text steps) into
// concrete checklist steps. Every step must be long enough to name an action,
// must not be a generic placeholder, and must not duplicate another step.
func validatedSteps(raw []planStepInput, fallback []PlanStep) ([]PlanStep, error) {
	steps := make([]PlanStep, 0, len(raw)+len(fallback))
	for _, s := range raw {
		if strings.TrimSpace(s.Goal) == "" {
			continue
		}
		steps = append(steps, PlanStep{Goal: strings.TrimSpace(s.Goal)})
	}
	if len(steps) == 0 {
		steps = append(steps, fallback...)
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("at least one concrete step is required: pass `steps` or non-empty `plan` text")
	}
	if len(steps) > maxPlanSteps {
		return nil, fmt.Errorf("too many steps (%d, max %d): group them into fewer concrete actions", len(steps), maxPlanSteps)
	}
	seen := map[string]int{}
	for i, s := range steps {
		if vagueStepPhrases[normalizePlanText(s.Goal)] {
			return nil, fmt.Errorf("step %d is too generic (%q): write a concrete action naming what to do, where, and the expected outcome", i+1, s.Goal)
		}
		if n := len([]rune(s.Goal)); n < minStepRunes {
			return nil, fmt.Errorf("step %d is too vague (%q, %d chars): write a concrete action naming what to do, where, and the expected outcome (min %d chars)", i+1, s.Goal, n, minStepRunes)
		}
		key := normalizePlanText(s.Goal)
		if prev, dup := seen[key]; dup {
			return nil, fmt.Errorf("steps %d and %d are duplicates (%q): merge or differentiate them", prev, i+1, s.Goal)
		}
		seen[key] = i + 1
	}
	return steps, nil
}

// normalizePlanText lowercases, trims space, and strips trailing punctuation
// so goal comparisons and vague-phrase lookups ignore surface differences.
func normalizePlanText(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.Trim(s, ".!… \t")
}

func (e *planningToolExecutor) executeCheck(call ToolCall) ToolResult {
	if !e.planned || len(e.steps) == 0 {
		return ToolResult{ID: call.ID, Content: "call the `plan_create` tool before `plan_check`", IsError: true}
	}
	if e.closed {
		return ToolResult{ID: call.ID, Content: fmt.Sprintf("plan is closed for goal %q and cannot be checked. Give the final answer now.", e.goal), IsError: true}
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

// splitPlanLines parses fallback free-text steps: each non-empty line becomes
// one step, with markdown bullets, ordered prefixes, and [ ]/[x] markers
// stripped. A pre-checked [x] line starts Done.
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
