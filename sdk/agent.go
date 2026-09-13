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
	Client          *RouterClient
	Tools           ToolExecutor
	MaxRetries      int
	DisablePlanning bool

	interruptMu sync.Mutex
	interrupts  map[string]context.CancelFunc
	interrupted map[string]bool
}

const (
	defaultAgentMaxRetries = 6
	maxRetryCooldown       = 96 * time.Second
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
	if ok {
		if a.interrupted == nil {
			a.interrupted = make(map[string]bool)
		}
		a.interrupted[sessionID] = true
	}
	a.interruptMu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

func (a *Agent) wasInterrupted(sessionID string) bool {
	if a == nil || sessionID == "" {
		return false
	}
	a.interruptMu.Lock()
	defer a.interruptMu.Unlock()
	return a.interrupted[sessionID]
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
		delete(a.interrupted, sessionID)
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
	turnStart := time.Now()
	if trace != nil {
		inner := trace
		trace = func(ctx context.Context, event TraceEvent) { event.Elapsed = time.Since(turnStart); inner(ctx, event) }
	}

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
		if a.wasInterrupted(session.ID()) {
			settleInterruptedTurn(session, before)
			break
		}
		if retryAfterAbort(err) {
			break
		}
		session.ReplaceHistory(before)
		if !retryableAgentError(ctx, err) || attempt == retries {
			break
		}

		delay := backoff.Delay(err)
		traceEvent(ctx, trace, TraceEvent{Stage: TraceRetryWait, Err: err, RetryAfter: delay})
		if err := waitRetry(ctx, delay); err != nil {
			if a.wasInterrupted(session.ID()) {
				settleInterruptedTurn(session, before)
			}
			traceEvent(ctx, trace, TraceEvent{Stage: TraceError, Err: err})
			return Response{}, err
		}
	}

	traceEvent(ctx, trace, TraceEvent{Stage: TraceError, Err: lastErr})
	if errors.Is(lastErr, context.Canceled) || errors.Is(lastErr, context.DeadlineExceeded) {
		return Response{}, lastErr
	}
	return Response{}, fmt.Errorf("%w: attempts=%d: %w", ErrAgentRetriesExhausted, retries+1, lastErr)
}

func (a *Agent) runAttempt(ctx context.Context, session *Session, user Turn, req Request, trace TraceFunc, backoff *retryBackoff, entry func(context.Context, Input) error) (Response, error) {
	executor := newPlanningToolExecutor(a.Tools)
	if a.DisablePlanning {
		executor = nil
	}
	session.Append(user)
	if req.Stream {
		return a.runStreamAttempt(ctx, session, req, trace, backoff, entry)
	}
	baseSystemPrompt := req.SystemPrompt
	planNudges := 0

	for {
		if err := ctx.Err(); err != nil {
			return Response{}, err
		}
		req.Messages = buildContextWindow(session.History(), defaultContextWindowTokens)
		if executor != nil {
			req.SystemPrompt = planningSystemPrompt(baseSystemPrompt) + executor.systemPromptExtra()
			req.Tools = executor.Definitions()
		} else {
			req.SystemPrompt = baseSystemPrompt
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
			if executor != nil && executor.HasIncomplete() && !executor.IsClosed() && planNudges < maxPlanIncompleteNudges {
				planNudges++
				session.Append(executor.planReminderTurn())
				continue
			}
			traceEvent(ctx, trace, TraceEvent{Stage: TraceResponse, Response: cloneResponseContent(resp)})
			return resp, nil
		}
		if executor == nil {
			for _, call := range resp.ToolCalls {
				if err := ctx.Err(); err != nil {
					return Response{}, err
				}
				callCopy := cloneToolCall(call)
				traceEvent(ctx, trace, TraceEvent{Stage: TraceToolCall, ToolCall: callCopy})
				traceEvent(ctx, trace, TraceEvent{Stage: TraceToolRunning, ToolCall: callCopy})
				result := a.Tools.Execute(ctx, call)
				if result.ID == "" {
					result.ID = call.ID
				}
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
			result := executor.Execute(ctx, call)
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
	executor := newPlanningToolExecutor(a.Tools)
	if a.DisablePlanning {
		executor = nil
	}
	baseSystemPrompt := req.SystemPrompt
	planNudges := 0
	for {
		if err := ctx.Err(); err != nil {
			return Response{}, err
		}
		req.Messages = buildContextWindow(session.History(), defaultContextWindowTokens)
		if executor != nil {
			req.SystemPrompt = planningSystemPrompt(baseSystemPrompt) + executor.systemPromptExtra()
			req.Tools = executor.Definitions()
		} else {
			req.SystemPrompt = baseSystemPrompt
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
			if executor != nil && executor.HasIncomplete() && !executor.IsClosed() && planNudges < maxPlanIncompleteNudges {
				planNudges++
				session.Append(executor.planReminderTurn())
				continue
			}
			traceEvent(ctx, trace, TraceEvent{Stage: TraceResponse, Response: cloneResponseContent(resp)})
			return resp, nil
		}
		if executor == nil {
			for _, call := range resp.ToolCalls {
				callCopy := cloneToolCall(call)
				traceEvent(ctx, trace, TraceEvent{Stage: TraceToolRunning, ToolCall: callCopy})
				result := a.Tools.Execute(ctx, call)
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
			continue
		}
		for _, call := range resp.ToolCalls {
			if err := ctx.Err(); err != nil {
				return Response{}, err
			}
			callCopy := cloneToolCall(call)
			traceEvent(ctx, trace, TraceEvent{Stage: TraceToolRunning, ToolCall: callCopy})
			result := executor.Execute(ctx, call)
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
