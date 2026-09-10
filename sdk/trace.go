package sdk

import (
	"context"
	"time"
)

type TraceStage string

const (
	TraceRequest         TraceStage = "request"
	TraceProviderReady   TraceStage = "provider_ready"
	TraceResponseText    TraceStage = "response_text"
	TraceResponseContent TraceStage = "response_content"
	TraceToolCall         TraceStage = "tool_call"
	TraceToolRunning      TraceStage = "tool_running"
	TraceToolResult       TraceStage = "tool_result"
	TraceResponse         TraceStage = "response"
	TraceRetryWait        TraceStage = "retry_wait"
	TraceError            TraceStage = "error"
)

type TraceEvent struct {
	Stage      TraceStage    `json:"stage"`
	Message    string        `json:"message,omitempty"`
	Response   *Response     `json:"response,omitempty"`
	ToolCall   *ToolCall    `json:"tool_call,omitempty"`
	ToolResult *ToolResult  `json:"tool_result,omitempty"`
	Text       string        `json:"text,omitempty"`
	Err        error         `json:"-"`
	RetryAfter time.Duration `json:"retry_after,omitempty"`
}

type TraceFunc func(context.Context, TraceEvent)

// TraceMessage returns the canonical, transport-neutral description of a lifecycle event.
func TraceMessage(event TraceEvent) string {
	if event.Message != "" {
		return event.Message
	}
	switch event.Stage {
	case TraceRequest:
		return "sending request to provider"
	case TraceProviderReady:
		return "provider accepted request; processing"
	case TraceResponseText:
		return "provider thinking"
	case TraceResponseContent:
		return "provider sending content"
	case TraceToolCall:
		return "tool call received"
	case TraceToolRunning:
		return "tool execution started"
	case TraceToolResult:
		if event.ToolResult != nil && event.ToolResult.IsError {
			return "tool execution failed"
		}
		return "tool execution succeeded"
	case TraceResponse:
		return "provider response complete"
	case TraceRetryWait:
		return "waiting before retry"
	case TraceError:
		return "provider request failed"
	default:
		return ""
	}
}
