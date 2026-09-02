package sdk

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrAgentMaxIterations    = errors.New("sdk: agent reached maximum iterations")
	ErrAgentRetriesExhausted = errors.New("sdk: agent retries exhausted")
)

type ToolExecutor interface {
	Definitions() []Tool
	Execute(context.Context, ToolCall) ToolResult
}

type Agent struct {
	Client        *RouterClient
	Tools         ToolExecutor
	MaxIterations int
	MaxRetries    int
}

const (
	defaultAgentMaxIterations = 20
	defaultAgentMaxRetries    = 2
)

func (a *Agent) RunTurn(ctx context.Context, session *Session, user Turn, req Request) (Response, error) {
	return a.runTurn(ctx, session, user, req, nil)
}

func (a *Agent) RunTurnWithTrace(ctx context.Context, session *Session, user Turn, req Request, trace TraceFunc) (Response, error) {
	return a.runTurn(ctx, session, user, req, trace)
}

func (a *Agent) runTurn(ctx context.Context, session *Session, user Turn, req Request, trace TraceFunc) (Response, error) {
	if a == nil || a.Client == nil || session == nil {
		return Response{}, errors.New("sdk: incomplete agent configuration")
	}

	before := session.History()
	limit := a.MaxIterations
	if limit <= 0 {
		limit = defaultAgentMaxIterations
	}
	retries := a.MaxRetries
	if retries < 0 {
		retries = 0
	}
	if a.MaxRetries == 0 {
		retries = defaultAgentMaxRetries
	}

	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		session.ReplaceHistory(before)
		resp, err := a.runAttempt(ctx, session, user, req, limit, trace)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		session.ReplaceHistory(before)
		if !retryableAgentError(ctx, err) || attempt == retries {
			break
		}
	}

	return Response{}, fmt.Errorf("%w: attempts=%d: %w", ErrAgentRetriesExhausted, retries+1, lastErr)
}

func (a *Agent) runAttempt(ctx context.Context, session *Session, user Turn, req Request, limit int, trace TraceFunc) (Response, error) {
	session.Append(user)

	for iteration := 0; iteration < limit; iteration++ {
		req.Messages = session.History()
		if a.Tools != nil {
			req.Tools = a.Tools.Definitions()
		}
		traceEvent(ctx, trace, TraceEvent{Stage: TraceRequest, Request: cloneRequest(req)})

		resp, err := a.Client.Generate(ctx, session, req)
		if err != nil {
			traceEvent(ctx, trace, TraceEvent{Stage: TraceError, Err: err})
			return Response{}, err
		}
		traceEvent(ctx, trace, TraceEvent{Stage: TraceResponse, Response: cloneResponse(resp)})
		commitResponse(session, resp)

		if len(resp.ToolCalls) == 0 {
			return resp, nil
		}
		if a.Tools == nil {
			for _, call := range resp.ToolCalls {
				traceEvent(ctx, trace, TraceEvent{Stage: TraceToolCall, ToolCall: cloneToolCall(call)})
				result := ToolResult{ID: call.ID, Content: "tool execution is not configured", IsError: true}
				traceEvent(ctx, trace, TraceEvent{Stage: TraceToolResult, ToolResult: cloneToolResult(result)})
				session.Append(Turn{Role: RoleToolResult, ToolResult: &result})
			}
			continue
		}
		for _, call := range resp.ToolCalls {
			traceEvent(ctx, trace, TraceEvent{Stage: TraceToolCall, ToolCall: cloneToolCall(call)})
			result := a.Tools.Execute(ctx, call)
			if result.ID == "" {
				result.ID = call.ID
			}
			traceEvent(ctx, trace, TraceEvent{Stage: TraceToolResult, ToolResult: cloneToolResult(result)})
			resultCopy := result
			session.Append(Turn{Role: RoleToolResult, ToolResult: &resultCopy})
		}
	}

	return Response{}, fmt.Errorf("%w: limit=%d", ErrAgentMaxIterations, limit)
}

func traceEvent(ctx context.Context, trace TraceFunc, event TraceEvent) {
	if trace != nil {
		trace(ctx, event)
	}
}

func cloneRequest(in Request) *Request {
	out := in
	out.Messages = append([]Turn(nil), in.Messages...)
	out.Tools = append([]Tool(nil), in.Tools...)
	return &out
}

func cloneResponse(in Response) *Response {
	out := in
	out.Content = append([]ContentPart(nil), in.Content...)
	out.ToolCalls = append([]ToolCall(nil), in.ToolCalls...)
	if in.Reasoning != nil {
		r := *in.Reasoning
		out.Reasoning = &r
	}
	return &out
}

func cloneToolCall(in ToolCall) *ToolCall {
	out := in
	return &out
}

func cloneToolResult(in ToolResult) *ToolResult {
	out := in
	return &out
}

func retryableAgentError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
