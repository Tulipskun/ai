package sdk

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	interrupted map[string]bool
}

const (
	defaultAgentMaxRetries = 6
	maxRetryCooldown       = 96 * time.Second
	maxMarkerNudges        = 2
)

func markerEnforced(req Request) bool { return strings.Contains(req.SystemPrompt, ReplyMarker) }

const agentPlanInstruction = `Before using any tool, create a concise execution plan for the user's goal.
Write the plan as plain text, using one step per line. Numbered or bulleted steps are preferred.
Do not use JSON, code fences, or tool calls while creating the plan.`
const agentStepInstruction = `Execution plan:
%s

Current step: %d/%d
Current step goal: %s
Work on this step only. You may use any available tools needed to complete it, including tools needed to inspect and fix errors caused by your work. If a command fails, diagnose and fix it in this same step and retry. Do not start unrelated work.
When the current step is complete, end your response with the exact marker %s.`
const agentPlanDoneMarker = "<<STEP_DONE>>"

func parseAgentPlan(text string) AgentPlan {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	text = strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(text, "```"), "```text"))
	if text == "" {
		return AgentPlan{}
	}
	var steps []AgentPlanStep
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if lower == "plan:" || lower == "execution plan:" {
			continue
		}
		line = stripAgentPlanPrefix(line)
		if line == "" {
			continue
		}
		steps = append(steps, AgentPlanStep{Goal: line})
	}
	return AgentPlan{Steps: steps}
}

func stripAgentPlanPrefix(line string) string {
	line = strings.TrimSpace(line)
	for len(line) > 0 && (line[0] == '-' || line[0] == '*') {
		line = strings.TrimSpace(line[1:])
	}
	if strings.HasPrefix(line, "•") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "•"))
	}
	if i := strings.IndexByte(line, '.'); i > 0 && allAgentPlanDigits(line[:i]) {
		return strings.TrimSpace(line[i+1:])
	}
	if i := strings.IndexByte(line, ')'); i > 0 && allAgentPlanDigits(line[:i]) {
		return strings.TrimSpace(line[i+1:])
	}
	lower := strings.ToLower(line)
	if strings.HasPrefix(lower, "step ") {
		if i := strings.IndexByte(line, ':'); i > 5 {
			return strings.TrimSpace(line[i+1:])
		}
	}
	return line
}

func allAgentPlanDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func createAgentPlan(ctx context.Context, client *RouterClient, session *Session, user Turn, req Request) (AgentPlan, error) {
	planReq := req
	planReq.Tools = nil
	planReq.Stream = false
	planReq.SystemPrompt = strings.TrimSpace(strings.Join([]string{req.SystemPrompt, agentPlanInstruction}, "\n\n"))
	planReq.Messages = buildContextWindow(append(cloneTurns(session.History()), cloneTurn(user)), defaultContextWindowTokens)
	resp, err := client.Generate(ctx, session, planReq)
	if err != nil {
		return AgentPlan{}, err
	}
	plan := parseAgentPlan(ResponseText(resp))
	if len(plan.Steps) == 0 {
		return AgentPlan{Steps: []AgentPlanStep{{Goal: userTurnText(user)}}}, nil
	}
	return plan, nil
}

func userTurnText(user Turn) string {
	var parts []string
	for _, part := range user.Content {
		if text := strings.TrimSpace(part.Text); text != "" {
			parts = append(parts, text)
		}
	}
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if text == "" {
		return "Complete the user's request."
	}
	return text
}

func injectAgentStepPrompt(req *Request, plan AgentPlan, current int) {
	if req == nil || current < 0 || current >= len(plan.Steps) {
		return
	}
	steps := make([]string, len(plan.Steps))
	for i, step := range plan.Steps {
		steps[i] = fmt.Sprintf("%d. %s", i+1, step.Goal)
	}
	req.SystemPrompt = strings.TrimSpace(strings.Join([]string{
		req.SystemPrompt,
		fmt.Sprintf(agentStepInstruction, strings.Join(steps, "\n"), current+1, len(plan.Steps), plan.Steps[current].Goal, agentPlanDoneMarker),
	}, "\n\n"))
}

func stepDone(resp Response) bool {
	return strings.Contains(ResponseText(resp), agentPlanDoneMarker)
}

func stripStepDoneMarker(resp Response) Response {
	for i := range resp.Content {
		if resp.Content[i].Type == ContentText {
			resp.Content[i].Text = strings.TrimSpace(strings.ReplaceAll(resp.Content[i].Text, agentPlanDoneMarker, ""))
		}
	}
	return resp
}

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
	nudges := 0
	var plan AgentPlan
	currentStep := 0
	if a.Tools != nil {
		var err error
		plan, err = createAgentPlan(ctx, a.Client, session, user, req)
		if err != nil {
			return Response{}, err
		}
	}
	session.Append(user)
	if req.Stream {
		return a.runStreamAttempt(ctx, session, req, plan, trace, backoff, entry)
	}
	baseSystemPrompt := req.SystemPrompt

	for {
		if err := ctx.Err(); err != nil {
			return Response{}, err
		}
		req.Messages = buildContextWindow(session.History(), defaultContextWindowTokens)
		if a.Tools != nil {
			req.SystemPrompt = baseSystemPrompt
			injectAgentStepPrompt(&req, plan, currentStep)
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
		stepFinished := a.Tools != nil && len(plan.Steps) > 0 && len(resp.ToolCalls) == 0 && stepDone(resp)
		if stepFinished {
			resp = stripStepDoneMarker(resp)
		}
		commitResponse(session, resp)

		if len(resp.Content) > 0 {
			traceEvent(ctx, trace, TraceEvent{Stage: TraceResponseContent, Response: cloneResponseContent(resp)})
		}

		if len(resp.ToolCalls) == 0 {
			if stepFinished {
				if currentStep+1 < len(plan.Steps) {
					currentStep++
					continue
				}
			}
			if nudges < maxMarkerNudges && markerEnforced(req) && len(resp.Content) > 0 && !HasReplyMarker(resp) {
				nudges++
				nudge := Turn{Role: RoleUser, Content: []ContentPart{{Type: ContentText, Text: markerNudgeText}}}
				session.Append(nudge)
				continue
			}
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

func (a *Agent) runStreamAttempt(ctx context.Context, session *Session, req Request, plan AgentPlan, trace TraceFunc, backoff *retryBackoff, entry func(context.Context, Input) error) (Response, error) {
	nudges := 0
	currentStep := 0
	baseSystemPrompt := req.SystemPrompt
	for {
		if err := ctx.Err(); err != nil {
			return Response{}, err
		}
		req.Messages = buildContextWindow(session.History(), defaultContextWindowTokens)
		if a.Tools != nil {
			req.SystemPrompt = baseSystemPrompt
			injectAgentStepPrompt(&req, plan, currentStep)
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
		stepFinished := a.Tools != nil && len(plan.Steps) > 0 && len(resp.ToolCalls) == 0 && stepDone(resp)
		if stepFinished {
			resp = stripStepDoneMarker(resp)
		}

		commitResponse(session, resp)
		if len(resp.ToolCalls) == 0 {
			if a.Tools != nil && len(plan.Steps) > 0 && stepDone(resp) {
				if currentStep+1 < len(plan.Steps) {
					currentStep++
					continue
				}
				resp = stripStepDoneMarker(resp)
			}
			if nudges < maxMarkerNudges && markerEnforced(req) && len(resp.Content) > 0 && !HasReplyMarker(resp) {
				nudges++
				nudge := Turn{Role: RoleUser, Content: []ContentPart{{Type: ContentText, Text: markerNudgeText}}}
				session.Append(nudge)
				continue
			}
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

// settleInterruptedTurn preserves a user-interrupted turn instead of rolling
// it back: everything committed so far stays, and every tool call that never
// got a result is closed with an interrupted failure so the history stays
// coherent for the next turn.
func settleInterruptedTurn(session *Session, before []Turn) {
	if session == nil {
		return
	}
	history := session.History()
	start := len(before)
	if start > len(history) {
		start = len(history)
	}
	pending := make(map[string]string)
	var order []string
	for _, turn := range history[start:] {
		if turn.Role == RoleToolCall && turn.ToolCall != nil && turn.ToolCall.ID != "" {
			if _, dup := pending[turn.ToolCall.ID]; !dup {
				pending[turn.ToolCall.ID] = turn.ToolCall.Name
				order = append(order, turn.ToolCall.ID)
			}
		}
		if turn.Role == RoleToolResult && turn.ToolResult != nil {
			delete(pending, turn.ToolResult.ID)
		}
	}
	for _, id := range order {
		name, ok := pending[id]
		if !ok {
			continue
		}
		if name == "" {
			name = "tool"
		}
		session.Append(Turn{Role: RoleToolResult, ToolResult: &ToolResult{ID: id, Content: "tool `" + name + "` was interrupted by the user; it may have partially run, not run at all, or already finished - verify the actual state before retrying.", IsError: true}})
	}
}

func traceEvent(ctx context.Context, trace TraceFunc, event TraceEvent) {
	if trace != nil {
		trace(ctx, event)
	}
}
func cloneResponseContent(in Response) *Response {
	return &Response{Provider: in.Provider, Model: in.Model, Content: append([]ContentPart(nil), in.Content...), Reasoning: in.Reasoning, Usage: in.Usage}
}
func cloneToolCall(in ToolCall) *ToolCall       { out := in; return &out }
func cloneToolResult(in ToolResult) *ToolResult { out := in; return &out }
func retryableAgentError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
func isRateLimitError(err error) bool {
	statusErr, ok := err.(HTTPStatusError)
	return ok && statusErr.HTTPStatusCode() == 429
}

type retryBackoff struct{ consecutive int }

func (b *retryBackoff) Reset() { b.consecutive = 0 }
func (b *retryBackoff) Delay(err error) time.Duration {
	b.consecutive++
	return retryDelay(err, b.consecutive)
}

// retryAfterAbort reports whether err carries a Retry-After beyond
// maxRetryCooldown: hour/day scale bans must stop, not retry.
func retryAfterAbort(err error) bool {
	if !isRateLimitError(err) {
		return false
	}
	ra, ok := err.(RetryAfterError)
	if !ok {
		return false
	}
	return ra.RetryAfter() > maxRetryCooldown
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
