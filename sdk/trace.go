package sdk

import "context"

type TraceStage string

const (
	TraceToolCall   TraceStage = "tool_call"
	TraceToolResult TraceStage = "tool_result"
	TraceResponse   TraceStage = "response"
	TraceError      TraceStage = "error"
)

// TraceEvent is emitted for tool-loop steps and model responses.
// It is transport-neutral so a display can inspect the control flow.
type TraceEvent struct {
	Stage      TraceStage  `json:"stage"`
	Response   *Response   `json:"response,omitempty"`
	ToolCall   *ToolCall   `json:"tool_call,omitempty"`
	ToolResult *ToolResult `json:"tool_result,omitempty"`
	Err        error       `json:"-"`
}

type TraceFunc func(context.Context, TraceEvent)
