package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const planningToolName = "plan"

const planningToolDescription = "Create the execution plan for the user's goal before delegating execution. Describe the intended work in concise, ordered steps. This is a planning action, not the final answer."

const planningSystemInstruction = "You are the Main Agent. These role boundaries take precedence over any conflicting direct-execution instructions in the supplied context; delegate such work to the Sub-agent. You do not have execution tools and must never attempt to inspect, read, edit, write, search, build, test, run commands, browse, or otherwise operate on the project directly. The Sub-agent is the only worker. First delegate repository investigation so the Sub-agent can inspect source code and repository requirements and return a concise summary. Use that summary to create one ordered plan with concrete steps. After the plan exists, delegate exactly one current plan step at a time with `delegate_to_subagent`. Wait for the orchestration lifecycle event reporting that step's completion or failure. Do not repeatedly poll status: wait for completion events; use `subagent_status` for explicit status requests. A worker loop ending, including a textual blocked or incomplete report, is NOT verified success and never advances the plan. Read the terminal result with `subagent_history` or `subagent_status` (`subagent_history` lists every worker tool with name, arguments/details, and result), verify the assigned work and validation, then call `accept_subagent_result` with verification evidence to advance exactly one step. For failed, stopped, blocked, or incomplete work use `follow_up_subagent` with the job ID to retry in the same worker session; for new follow-on work use `continue_subagent` with the job ID to keep the same worker session and its history, and wait for its new completion event. After acceptance, delegate the next step or continue the same worker session. Stale events from replacement plans must not be accepted. A completed plan allows investigation or continued work for the next user task. The Sub-agent does not communicate with the user. Do not expose internal planning or orchestration details to the user."

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

func planningSystemPrompt(base string) string {
	// Keep repository requirements and custom context intact. The appended role
	// boundary and executor allowlist supersede conflicting execution guidance.
	base = strings.TrimSpace(base)
	if base == "" {
		return planningSystemInstruction
	}
	return base + "\n\n" + planningSystemInstruction
}

func (e *planningToolExecutor) Execute(ctx context.Context, call ToolCall) ToolResult {
	if e == nil {
		return ToolResult{ID: call.ID, Content: "tool execution is not configured", IsError: true}
	}
	if call.Name == "delegate_to_subagent" || call.Name == "subagent_status" || call.Name == "subagent_history" || call.Name == "stop_subagent" || call.Name == "follow_up_subagent" || call.Name == "continue_subagent" || call.Name == "accept_subagent_result" {
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
		return ToolResult{ID: call.ID, Content: "Execution plan recorded with " + fmt.Sprint(len(steps)) + " ordered step(s). Start with step 1 and advance only after reviewing and explicitly accepting verified success with accept_subagent_result."}
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
