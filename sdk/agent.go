package sdk

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
	Client     *RouterClient
	Tools      ToolExecutor
	MaxRetries int

	interruptMu sync.Mutex
	interrupts  map[string]context.CancelFunc
}

const (
	defaultAgentMaxRetries = 2
	maxRetryCooldown        = 96 * time.Second
)

func (a *Agent) RunTurn(ctx context.Context, session *Session, user Turn, req Request) (Response, error) {
	return a.runTurn(ctx, session, user, req, nil, nil)
}

func (a *Agent) RunTurnWithTrace(ctx context.Context, session *Session, user Turn, req Request, trace TraceFunc) (Response, error) {
	return a.runTurn(ctx, session, user, req, trace, nil)
}

func (a *Agent) RunTurnWithTraceAndEntry(ctx context.Context, session *Session, user Turn, req Request, trace TraceFunc, entry func(context.Context, Input) error) (Response, error) {
	return a.runTurn(ctx, session, user, req, trace, entry)
}

func (a *Agent) Interrupt(sessionID string) bool {
	if a == nil || sessionID == "" {
		return false
	}
	a.interruptMu.Lock()
	cancel, ok := a.interrupts[sessionID]
	a.interruptMu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

func (a *Agent) beginInterrupt(ctx context.Context, sessionID string) (context.Context, func()) {
	turnCtx, cancel := context.WithCancel(ctx)
	if sessionID == "" {
		return turnCtx, cancel
	}
	a.interruptMu.Lock()
	if a.interrupts == nil {
		a.interrupts = make(map[string]context.CancelFunc)
	}
	a.interrupts[sessionID] = cancel
	a.interruptMu.Unlock()
	return turnCtx, func() {
		a.interruptMu.Lock()
		delete(a.interrupts, sessionID)
		a.interruptMu.Unlock()
		cancel()
	}
}

func (a *Agent) runTurn(ctx context.Context, session *Session, user Turn, req Request, trace TraceFunc, entry func(context.Context, Input) error) (Response, error) {
	if a == nil || a.Client == nil || session == nil {
		return Response{}, errors.New("sdk: incomplete agent configuration")
	}
	ctx, cleanup := a.beginInterrupt(ctx, session.ID())
	defer cleanup()
	turnStart:=time.Now()
	if trace!=nil{inner:=trace;trace=func(ctx context.Context,event TraceEvent){event.Elapsed=time.Since(turnStart);inner(ctx,event)}}

	before := session.History()
	retries := a.MaxRetries
	if retries < 0 {
		retries = 0
	}
	if a.MaxRetries == 0 {
		retries = defaultAgentMaxRetries
	}

	backoff := retryBackoff{}
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		session.ReplaceHistory(before)
		resp, err := a.runAttempt(ctx, session, user, req, trace, &backoff, entry)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		session.ReplaceHistory(before)
		if !retryableAgentError(ctx, err) || attempt == retries {
			break
		}

		delay := backoff.Delay(err)
		traceEvent(ctx, trace, TraceEvent{Stage: TraceRetryWait, Err: err, RetryAfter: delay})
		if err := waitRetry(ctx, delay); err != nil {
			return Response{}, err
		}
	}

	if errors.Is(lastErr, context.Canceled) || errors.Is(lastErr, context.DeadlineExceeded) {
		return Response{}, lastErr
	}
	return Response{}, fmt.Errorf("%w: attempts=%d: %w", ErrAgentRetriesExhausted, retries+1, lastErr)
}

func (a *Agent) runAttempt(ctx context.Context, session *Session, user Turn, req Request, trace TraceFunc, backoff *retryBackoff, entry func(context.Context, Input) error) (Response, error) {
	session.Append(user)
	if req.Stream {
		return a.runStreamAttempt(ctx, session, req, trace, backoff, entry)
	}

	for {
		if err := ctx.Err(); err != nil {
			return Response{}, err
		}
		req.Messages = buildContextWindow(session.History(), defaultContextWindowTokens)
		if a.Tools != nil {
			req.Tools = a.Tools.Definitions()
		}
		traceEvent(ctx, trace, TraceEvent{Stage: TraceRequest})

		resp, err := a.Client.Generate(ctx, session, req)
		if err != nil {
			return Response{}, err
		}
		traceEvent(ctx, trace, TraceEvent{Stage: TraceProviderReady})
		if err := ctx.Err(); err != nil {
			return Response{}, err
		}
		if backoff != nil {
			backoff.Reset()
		}
		commitResponse(session, resp)

		if len(resp.Content) > 0 {
			traceEvent(ctx, trace, TraceEvent{Stage: TraceResponseContent, Response: cloneResponseContent(resp)})
		}

		if len(resp.ToolCalls) == 0 {
			traceEvent(ctx, trace, TraceEvent{Stage: TraceResponse, Response: cloneResponseContent(resp)})
			return resp, nil
		}
		if a.Tools == nil {
			for _, call := range resp.ToolCalls {
				if err := ctx.Err(); err != nil {
					return Response{}, err
				}
				callCopy := cloneToolCall(call)
				traceEvent(ctx, trace, TraceEvent{Stage: TraceToolCall, ToolCall: callCopy})
				traceEvent(ctx, trace, TraceEvent{Stage: TraceToolRunning, ToolCall: callCopy})
				result := ToolResult{ID: call.ID, Content: "tool execution is not configured", IsError: true}
				traceEvent(ctx, trace, TraceEvent{Stage: TraceToolResult, ToolCall: callCopy, ToolResult: cloneToolResult(result)})
				if entry != nil {
					if err := entry(ctx, Input{Source: "tool", SessionID: session.ID(), Turn: Turn{Role: RoleToolResult, ToolResult: cloneToolResult(result)}}); err != nil {
						return Response{}, err
					}
				} else {
					resultCopy := result
					session.Append(Turn{Role: RoleToolResult, ToolResult: &resultCopy})
				}
			}
			continue
		}
		for _, call := range resp.ToolCalls {
			if err := ctx.Err(); err != nil {
				return Response{}, err
			}
			callCopy := cloneToolCall(call)
			traceEvent(ctx, trace, TraceEvent{Stage: TraceToolCall, ToolCall: callCopy})
			traceEvent(ctx, trace, TraceEvent{Stage: TraceToolRunning, ToolCall: callCopy})
			result := a.Tools.Execute(ctx, call)
			if err := ctx.Err(); err != nil {
				return Response{}, err
			}
			if result.ID == "" {
				result.ID = call.ID
			}
			traceEvent(ctx, trace, TraceEvent{Stage: TraceToolResult, ToolCall: callCopy, ToolResult: cloneToolResult(result)})
			resultCopy := result
			if entry != nil {
				if err := entry(ctx, Input{Source: "tool", SessionID: session.ID(), Turn: Turn{Role: RoleToolResult, ToolResult: &resultCopy}}); err != nil {
					return Response{}, err
				}
			} else {
				session.Append(Turn{Role: RoleToolResult, ToolResult: &resultCopy})
			}
		}
	}
}

func (a *Agent) runStreamAttempt(ctx context.Context, session *Session, req Request, trace TraceFunc, backoff *retryBackoff, entry func(context.Context, Input) error) (Response, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Response{}, err
		}
		req.Messages = buildContextWindow(session.History(), defaultContextWindowTokens)
		if a.Tools != nil {
			req.Tools = a.Tools.Definitions()
		}
		traceEvent(ctx, trace, TraceEvent{Stage: TraceRequest})

		events, err := a.Client.Stream(ctx, session, req)
		if err != nil {
			return Response{}, err
		}
		traceEvent(ctx, trace, TraceEvent{Stage: TraceProviderReady})

		var resp Response
		var text []ContentPart
		var calls []ToolCall
		var reasoning *ReasoningState
		var streamErr error
		for event := range events {
			switch event.Type {
			case EventText:
				if event.Text != "" {
					text = append(text, ContentPart{Type: ContentText, Text: event.Text})
					traceEvent(ctx, trace, TraceEvent{Stage: TraceResponseContent, Text: event.Text})
				}
			case EventReasoning:
				if event.Reasoning != nil {
					r := *event.Reasoning
					if reasoning == nil {
						reasoning = &ReasoningState{}
					}
					if r.ID != "" {
						reasoning.ID = r.ID
					}
					reasoning.Text += r.Text
					if r.Text != "" {
						traceEvent(ctx, trace, TraceEvent{Stage: TraceResponseText, Text: r.Text})
					}
				}
			case EventToolCall:
				if event.ToolCall != nil {
					call := *event.ToolCall
					calls = append(calls, call)
					traceEvent(ctx, trace, TraceEvent{Stage: TraceToolCall, ToolCall: cloneToolCall(call)})
				}
			case EventDone:
				if event.Response != nil {
					resp = *event.Response
				}
			case EventError:
				streamErr = event.Err
			}
			if event.Type == EventError {
				break
			}
		}
		if streamErr != nil {
			return Response{}, streamErr
		}
		if resp.Provider == "" {
			resp.Provider = string(session.Config().Provider)
		}
		if resp.Model == "" {
			resp.Model = session.Config().Model
		}
		if len(resp.Content) == 0 {
			resp.Content = text
		}
		if len(resp.ToolCalls) == 0 {
			resp.ToolCalls = calls
		}
		if resp.Reasoning == nil {
			resp.Reasoning = reasoning
		}
		if backoff != nil {
			backoff.Reset()
		}

		commitResponse(session, resp)
		if len(resp.ToolCalls) == 0 {
			traceEvent(ctx, trace, TraceEvent{Stage: TraceResponse, Response: cloneResponseContent(resp)})
			return resp, nil
		}
		if a.Tools == nil {
			for _, call := range resp.ToolCalls {
				callCopy := cloneToolCall(call)
				traceEvent(ctx, trace, TraceEvent{Stage: TraceToolRunning, ToolCall: callCopy})
				result := ToolResult{ID: call.ID, Content: "tool execution is not configured", IsError: true}
				traceEvent(ctx, trace, TraceEvent{Stage: TraceToolResult, ToolCall: callCopy, ToolResult: cloneToolResult(result)})
				resultCopy := result
				if entry != nil {
					if err := entry(ctx, Input{Source: "tool", SessionID: session.ID(), Turn: Turn{Role: RoleToolResult, ToolResult: &resultCopy}}); err != nil {
						return Response{}, err
					}
				} else {
					session.Append(Turn{Role: RoleToolResult, ToolResult: &resultCopy})
				}
			}
			continue
		}
		for _, call := range resp.ToolCalls {
			if err := ctx.Err(); err != nil {
				return Response{}, err
			}
			callCopy := cloneToolCall(call)
			traceEvent(ctx, trace, TraceEvent{Stage: TraceToolRunning, ToolCall: callCopy})
			result := a.Tools.Execute(ctx, call)
			if err := ctx.Err(); err != nil {
				return Response{}, err
			}
			if result.ID == "" {
				result.ID = call.ID
			}
			traceEvent(ctx, trace, TraceEvent{Stage: TraceToolResult, ToolCall: callCopy, ToolResult: cloneToolResult(result)})
			resultCopy := result
			if entry != nil {
				if err := entry(ctx, Input{Source: "tool", SessionID: session.ID(), Turn: Turn{Role: RoleToolResult, ToolResult: &resultCopy}}); err != nil {
					return Response{}, err
				}
			} else {
				session.Append(Turn{Role: RoleToolResult, ToolResult: &resultCopy})
			}
		}
	}
}

func traceEvent(ctx context.Context, trace TraceFunc, event TraceEvent) {
	if trace != nil {
		trace(ctx, event)
	}
}
func cloneResponseContent(in Response) *Response {
	return &Response{Provider: in.Provider, Model: in.Model, Content: append([]ContentPart(nil), in.Content...), Reasoning: in.Reasoning}
}
func cloneToolCall(in ToolCall) *ToolCall { out := in; return &out }
func cloneToolResult(in ToolResult) *ToolResult { out := in; return &out }
func retryableAgentError(ctx context.Context, err error) bool { if err == nil || ctx.Err() != nil { return false }; return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) }
func isRateLimitError(err error) bool { statusErr, ok := err.(HTTPStatusError); return ok && statusErr.HTTPStatusCode() == 429 }
type retryBackoff struct { consecutive int }
func (b *retryBackoff) Reset() { b.consecutive = 0 }
func (b *retryBackoff) Delay(err error) time.Duration { b.consecutive++; return retryDelay(err, b.consecutive) }
func retryDelay(err error, attempt int) time.Duration { if isRateLimitError(err) { if retryAfter, ok := err.(RetryAfterError); ok { if d := retryAfter.RetryAfter(); d > 0 { if d > maxRetryCooldown { return maxRetryCooldown }; return d } } }; if attempt <= 1 { return 3 * time.Second }; d := 3 * time.Second; for i := 1; i < attempt; i++ { if d >= maxRetryCooldown { return maxRetryCooldown }; d *= 2; if d > maxRetryCooldown { return maxRetryCooldown } }; return d }
func waitRetry(ctx context.Context, delay time.Duration) error { t := time.NewTimer(delay); defer t.Stop(); select { case <-ctx.Done(): return ctx.Err(); case <-t.C: return nil }
}
