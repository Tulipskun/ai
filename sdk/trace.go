package sdk

import "context"

type TraceStage string

const (
	TraceRequest    TraceStage = "request"
	TraceResponse   TraceStage = "response"
	TraceToolCall   TraceStage = "tool_call"
	TraceToolResult TraceStage = "tool_result"
	TraceError      TraceStage = "error"
)

// TraceEvent is emitted for each model request/response and tool-loop step.
// It is transport-neutral so a display can inspect the complete control flow.
type TraceEvent struct {
	Stage      TraceStage `json:"stage"`
	Request    *Request   `json:"request,omitempty"`
	Response   *Response  `json:"response,omitempty"`
	ToolCall   *ToolCall  `json:"tool_call,omitempty"`
	ToolResult *ToolResult `json:"tool_result,omitempty"`
	Err        error      `json:"-"`
}

type TraceFunc func(context.Context, TraceEvent)
