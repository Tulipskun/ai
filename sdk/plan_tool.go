package sdk

import (
	"context"
	"encoding/json"
	"strings"
)

const planningToolName = "plan"

const planningToolDescription = "Create the execution plan for the user's goal before using any execution tool. Describe the intended work in concise, ordered steps. This is a planning action, not the final answer."

const planningSystemInstruction = "For tasks that require work with tools, call the `plan` tool first. Use the returned plan as the basis for execution, then call the tools needed to complete it. Do not expose tool-call arguments or internal planning format as the user-facing answer."

type planningToolInput struct {
	Plan string `json:"plan"`
}

type planningToolExecutor struct {
	base    ToolExecutor
	planned bool
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
	for _, d := range defs {
		if d.Name == planningToolName {
			return defs
		}
	}
	return append(defs, Tool{
		Name:        planningToolName,
		Description: planningToolDescription,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"plan": map[string]any{"type": "string"},
			},
			"required": []string{"plan"},
		},
	})
}

func planningSystemPrompt(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return planningSystemInstruction
	}
	return base + "\n\n" + planningSystemInstruction
}

func (e *planningToolExecutor) Execute(ctx context.Context, call ToolCall) ToolResult {
	if e == nil || e.base == nil {
		return ToolResult{ID: call.ID, Content: "tool execution is not configured", IsError: true}
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
		e.planned = true
		return ToolResult{ID: call.ID, Content: "Execution plan recorded. Continue by carrying out the planned steps."}
	}
	if !e.planned {
		return ToolResult{ID: call.ID, Content: "call the `plan` tool before using execution tools", IsError: true}
	}
	return e.base.Execute(ctx, call)
}
