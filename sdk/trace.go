package sdk

import (
	"context"
	"time"
)

type TraceStage string

const (
	TraceToolCall    TraceStage = "tool_call"
	TraceToolResult  TraceStage = "tool_result"
	TraceResponse    TraceStage = "response"
	TraceResponseText TraceStage = "response_text"
	TraceRetryWait   TraceStage = "retry_wait"
	TraceError       TraceStage = "error"
)

type TraceEvent struct {
	Stage       TraceStage    `json:"stage"`
	Response    *Response     `json:"response,omitempty"`
	ToolCall    *ToolCall     `json:"tool_call,omitempty"`
	ToolResult  *ToolResult   `json:"tool_result,omitempty"`
	Text        string        `json:"text,omitempty"`
	Err         error         `json:"-"`
	RetryAfter  time.Duration `json:"retry_after,omitempty"`
}

type TraceFunc func(context.Context, TraceEvent)
