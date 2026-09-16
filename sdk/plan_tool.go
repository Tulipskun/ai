package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const planningToolName = "plan"

const planningToolDescription = "Create or replace the execution checklist for the user's goal before delegating execution. Describe the intended work in concise, ordered steps; the checklist with per-step status is shown to you on every turn. This is a planning action, not the final answer."

const planningSystemInstruction = "You are the Main Agent. You are the planner, not a courier: never hand the user's raw message to the worker as its task - analyze the goal, break it into minimal ordered steps, and write each delegated task in your own words with scope, expected outcome, and validation requirement. All planner-to-worker traffic (task text, follow-ups, and reports) must be in English regardless of the user's language; answer the user in their own language. You hold the project overview and own the checklist: every turn shows the current plan steps with their statuses, and only you advance them through explicit acceptance. These role boundaries take precedence over any conflicting direct-execution instructions in the supplied context; delegate such work to the Sub-agent. You do not have execution tools and must never attempt to inspect, read, edit, write, search, build, test, run commands, browse, or otherwise operate on the project directly. The Sub-agent is the only worker. Scale effort to the task: delegate repository investigation first only when the task needs repository context; for a trivial task that needs no repository context, skip the separate investigation and make a minimal one-step plan. Keep every plan to the fewest steps that cover the goal. Use the investigation summary to create one ordered plan with concrete steps. After the plan exists, delegate exactly one current plan step at a time with `delegate_to_subagent`, and write each delegated task so the worker validates with the minimal sufficient check only. Delegation returns control to you immediately with a job id. Reports then arrive on their own: a progress report after every few completed worker tool calls (use it for one quick scope check - over/under/off-target work; if wrong, call `stop_subagent`, which blocks until the worker really stops, then `follow_up_subagent` with the corrected task; if correct, answer briefly and stop calling tools so you wait cheaply), and the complete handoff report (terminal status, final summary, every worker tool with arguments and result) when the job ends. There are no status or history polling tools. A worker loop ending, including a textual blocked or incomplete report, is NOT verified success and never advances the plan. Verify the report, then call `accept_subagent_result` with verification evidence to advance exactly one step. For failed, blocked, or incomplete work use `follow_up_subagent` with the job ID to retry in the same worker session (returns a new job id; its reports arrive the same way); for new follow-on work use `continue_subagent` with the job ID to keep the same worker session and its history. `stop_subagent` blocks until the worker has actually stopped and returns its final partial report. After acceptance, delegate the next step or continue the same worker session. Stale results from replacement plans must not be accepted. Keep tool calls minimal: delegate once, react only to the automatic reports, one acceptance per step."

type planningToolInput struct {
	Plan string `json:"plan"`
}

type planningToolExecutor struct {
	base     ToolExecutor
	planned  bool
	subAgent SubAgentRunner
	session  *Session
}

func newPlanningToolExecutor(base ToolExecutor, session *Session) *planningToolExecutor {
	return &planningToolExecutor{base: base, session: session}
}

func (e *planningToolExecutor) ConfigureSubAgent(runner SubAgentRunner) {
	if e != nil {
		e.subAgent = runner
	}
}

func (e *planningToolExecutor) Definitions() []Tool {
	if e == nil {
		return nil
	}
	defs := []Tool{{
		Name: planningToolName, Description: planningToolDescription, InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{"plan": map[string]any{"type": "string"}}, "required": []string{"plan"},
		},
	}}
	if e.subAgent != nil {
		defs = append(defs, (&subAgentTool{runner: e.subAgent}).Definitions()...)
	}
	return defs
}

func planningSystemPrompt(base string, plan PlanState) string {
	// Keep repository requirements and custom context intact. The appended role
	// boundary and executor allowlist supersede conflicting execution guidance.
	// The checklist rides every request so the planner always sees the whole
	// project and each step's status (REQ-016, REQ-034).
	base = strings.TrimSpace(base)
	prompt := planningSystemInstruction
	if len(plan.Steps) > 0 {
		prompt = "Current checklist (you own these statuses; they advance only through `accept_subagent_result`):\n" + formatPlanStatus(plan) + "\n\n" + prompt
	}
	if base == "" {
		return prompt
	}
	return base + "\n\n" + prompt
}

func formatPlanStatus(plan PlanState) string {
	var b strings.Builder
	for _, step := range plan.Steps {
		marker := "[ ]"
		switch step.Status {
		case "completed":
			marker = "[x]"
		case "ready":
			marker = "[>]"
		case "running":
			marker = "[~]"
		case "awaiting_review":
			marker = "[?]"
		case "failed":
			marker = "[!]"
		}
		fmt.Fprintf(&b, "%s %d. %s (%s)\n", marker, step.Index, step.Text, step.Status)
	}
	return strings.TrimSpace(b.String())
}

func (e *planningToolExecutor) Execute(ctx context.Context, call ToolCall) ToolResult {
	if e == nil {
		return ToolResult{ID: call.ID, Content: "tool execution is not configured", IsError: true}
	}
	if call.Name == "delegate_to_subagent" || call.Name == "stop_subagent" || call.Name == "follow_up_subagent" || call.Name == "continue_subagent" || call.Name == "accept_subagent_result" {
		if e.subAgent == nil {
			return ToolResult{ID: call.ID, Content: "sub-agent is not configured", IsError: true}
		}
		return (&subAgentTool{runner: e.subAgent}).Execute(ctx, call)
	}
	if call.Name == planningToolName {
		var input planningToolInput
		if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
			return ToolResult{ID: call.ID, Content: "invalid plan tool arguments", IsError: true}
		}
		input.Plan = strings.TrimSpace(input.Plan)
		if input.Plan == "" {
			return ToolResult{ID: call.ID, Content: "plan is required", IsError: true}
		}
		steps := parsePlanSteps(input.Plan)
		if len(steps) == 0 {
			return ToolResult{ID: call.ID, Content: "plan must contain at least one step", IsError: true}
		}
		if e.session != nil {
			e.session.cleanPlan(steps)
		}
		e.planned = true
		return ToolResult{ID: call.ID, Content: "Checklist recorded with " + fmt.Sprint(len(steps)) + " ordered step(s); statuses are shown in your system prompt every turn. Start with step 1: delegate it and advance only after reviewing the blocking report and accepting verified success with accept_subagent_result."}
	}
	return ToolResult{ID: call.ID, Content: "Main Agent has no execution tools; delegate project work to `delegate_to_subagent`", IsError: true}
}

func parsePlanSteps(plan string) []string {
	lines := strings.Split(plan, "\n")
	steps := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "- *")
		line = strings.TrimSpace(line)
		for len(line) > 0 && line[0] >= '0' && line[0] <= '9' {
			line = line[1:]
		}
		line = strings.TrimSpace(strings.TrimLeft(line, ".):"))
		if line != "" {
			steps = append(steps, line)
		}
	}
	if len(steps) == 0 && strings.TrimSpace(plan) != "" {
		return []string{strings.TrimSpace(plan)}
	}
	return steps
}
