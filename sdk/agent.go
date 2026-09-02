package sdk

import (
	"context"
	"errors"
	"fmt"
	"time"
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
	defaultAgentMaxRetries    = 6
	maxRetryCooldown          = 96 * time.Second
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

		if isRateLimitError(err) {
			if err := a.rotateKeyIfConfigured(session); err != nil {
				return Response{}, fmt.Errorf("%w: rotate key: %v", ErrAgentRetriesExhausted, err)
			}
		}

		delay := retryDelay(err, attempt+1)
		traceEvent(ctx, trace, TraceEvent{Stage: TraceRetryWait, Err: err, RetryAfter: delay})
		if err := waitRetry(ctx, delay); err != nil {
			return Response{}, err
		}
	}

	return Response{}, fmt.Errorf("%w: attempts=%d: %w", ErrAgentRetriesExhausted, retries+1, lastErr)
}

func (a *Agent) rotateKeyIfConfigured(session *Session) error {
	provider, err := a.Client.Router.Provider(session.Config().Provider)
	if err != nil || !provider.RotateKeys {
		return err
	}
	if provider.Keys == nil || provider.Keys.Len() < 2 {
		return nil
	}
	_, err = session.RotateAPIKey()
	return err
}

func (a *Agent) runAttempt(ctx context.Context, session *Session, user Turn, req Request, limit int, trace TraceFunc) (Response, error) {
	session.Append(user)

	for iteration := 0; iteration < limit; iteration++ {
		req.Messages = buildContextWindow(session.History(), defaultContextWindowTokens)
		if a.Tools != nil {
			req.Tools = a.Tools.Definitions()
		}

		resp, err := a.Client.Generate(ctx, session, req)
		if err != nil {
			return Response{}, err
		}
		commitResponse(session, resp)

		if len(resp.Content) > 0 {
			traceEvent(ctx, trace, TraceEvent{Stage: TraceResponse, Response: cloneResponseContent(resp)})
		}

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

func cloneResponseContent(in Response) *Response {
	out := Response{Content: append([]ContentPart(nil), in.Content...)}
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

func isRateLimitError(err error) bool {
	statusErr, ok := err.(HTTPStatusError)
	if !ok {
		return false
	}
	return statusErr.HTTPStatusCode() == 429
}

func retryDelay(err error, attempt int) time.Duration {
	if isRateLimitError(err) {
		if retryAfter, ok := err.(RetryAfterError); ok {
			if d := retryAfter.RetryAfter(); d > 0 {
				if d > maxRetryCooldown {
					return maxRetryCooldown
				}
				return d
			}
		}
	}

	if attempt <= 1 {
		return 3 * time.Second
	}
	d := 3 * time.Second
	for i := 1; i < attempt; i++ {
		if d >= maxRetryCooldown {
			return maxRetryCooldown
		}
		d *= 2
		if d > maxRetryCooldown {
			return maxRetryCooldown
		}
	}
	return d
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
