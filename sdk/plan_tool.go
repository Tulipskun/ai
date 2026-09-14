package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const planningToolName = "plan"

const planningToolDescription = "Create the execution plan for the user's goal before using any execution tool. Describe the intended work in concise, ordered steps. This is a planning action, not the final answer."

const planningSystemInstruction = "You are the Main Agent. You do not have execution tools and must never attempt to inspect, read, edit, write, search, build, test, run commands, browse, or otherwise operate on the project directly. Use the Sub-agent as the only worker. First delegate repository investigation so the Sub-agent can inspect source code and repository requirements and return a concise summary. Use that summary to create one ordered plan with concrete steps. After the plan exists, delegate exactly one current plan step at a time with `delegate_to_subagent`. Wait for the orchestration lifecycle event reporting that step's completion or failure. On failure, analyze the report and retry or revise the same current step. Do not advance until it succeeds. After success, delegate the next step. Use `subagent_status` or `subagent_history` when more information is needed. Never communicate directly with the user about Sub-agent work until the overall task is complete. Do not expose internal planning or orchestration details to the user."

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
	if base == nil {
		return nil
	}
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
	if e.subAgent != nil {
		defs := []Tool{{
			Name: planningToolName, Description: planningToolDescription, InputSchema: map[string]any{
				"type": "object", "properties": map[string]any{"plan": map[string]any{"type": "string"}}, "required": []string{"plan"},
			},
		}}
		defs = append(defs, (&subAgentTool{runner: e.subAgent}).Definitions()...)
		return defs
	}
	if e.base == nil {
		return nil
	}
	defs := append([]Tool(nil), e.base.Definitions()...)
	defs = append(defs, Tool{
		Name: planningToolName, Description: planningToolDescription, InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{"plan": map[string]any{"type": "string"}}, "required": []string{"plan"},
		},
	})
	return defs
}

func planningSystemPrompt(base string) string {
	base = sanitizeMainAgentPrompt(base)
	if base == "" {
		return planningSystemInstruction
	}
	return base + "\n\n" + planningSystemInstruction
}

func sanitizeMainAgentPrompt(base string) string {
	lines := strings.Split(strings.TrimSpace(base), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "project requirements (repository source of truth):") || strings.HasPrefix(lower, "available tools:") {
			break
		}
		if containsMainAgentExecutionInstruction(lower) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func containsMainAgentExecutionInstruction(line string) bool {
	forbidden := []string{
		"call the matching tool",
		"inspect with read_file",
		"list_directory",
		"search_files",
		"use run_command",
		"use web_fetch",
		"use browser_",
	}
	for _, token := range forbidden {
		if strings.Contains(line, token) {
			return true
		}
	}
	return false
}

func (e *planningToolExecutor) Execute(ctx context.Context, call ToolCall) ToolResult {
	if e == nil {
		return ToolResult{ID: call.ID, Content: "tool execution is not configured", IsError: true}
	}
	if call.Name == "delegate_to_subagent" || call.Name == "subagent_status" || call.Name == "subagent_history" || call.Name == "stop_subagent" {
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
		return ToolResult{ID: call.ID, Content: "Execution plan recorded with " + fmt.Sprint(len(steps)) + " ordered step(s). Start with step 1 and advance only after successful completion."}
	}
	if !e.planned {
		return ToolResult{ID: call.ID, Content: "call the `plan` tool before using execution tools", IsError: true}
	}
	if e.subAgent != nil {
		return ToolResult{ID: call.ID, Content: "Main Agent has no execution tools; delegate the current plan step to `delegate_to_subagent`", IsError: true}
	}
	if e.base == nil {
		return ToolResult{ID: call.ID, Content: "tool execution is not configured", IsError: true}
	}
	return e.base.Execute(ctx, call)
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
