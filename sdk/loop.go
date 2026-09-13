package sdk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type SessionResolver func(context.Context, Input) (*Session, error)
type RequestResolver func(context.Context, Input, *Session) (Request, error)

type HarnessLoop struct {
	Client         *RouterClient
	Agent          *Agent
	Source         InputSource
	ResolveSession SessionResolver
	BuildRequest   RequestResolver
	Displays       []Display
	DisplayTimeout time.Duration
	OnTurnError    func(Input, error)

	sessionLocks sync.Map
}

func (h *HarnessLoop) Run(ctx context.Context) error {
	if h == nil || (h.Client == nil && h.Agent == nil) || h.Source == nil || h.ResolveSession == nil {
		return errors.New("sdk: incomplete harness loop configuration")
	}
	if h.Agent != nil {
		h.Agent.SetSubAgentEventSink(func(event SubAgentEvent) {
			if event.Parent == nil {
				return
			}
			stepLabel := "investigation"
			if event.PlanStep.Index > 0 {
				stepLabel = fmt.Sprintf("plan step %d", event.PlanStep.Index)
			}
			text := fmt.Sprintf("Sub-agent job %s %s for %s: %s", event.JobID, event.Status, stepLabel, event.Result)
			source := inputSourceForSession(event.Parent.ID())
			go func() {
				_ = h.Entry(context.WithoutCancel(ctx), Input{Source: source, SessionID: event.Parent.ID(), Turn: Turn{Role: RoleUser, Content: []ContentPart{{Type: ContentText, Text: text}}}})
			}()
		})
	}
	inputs, err := h.Source.Receive(ctx)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case input, ok := <-inputs:
			if !ok {
				return nil
			}
			if err := h.Entry(ctx, input); err != nil && h.OnTurnError != nil {
				h.OnTurnError(input, err)
			}
		}
	}
}

// Entry is the single ingress for both user input and tool results.
// SessionID identifies the session that owns the input.
func (h *HarnessLoop) Entry(ctx context.Context, input Input) error {
	if h == nil || (h.Client == nil && h.Agent == nil) || h.ResolveSession == nil {
		return errors.New("sdk: incomplete harness loop configuration")
	}
	session, err := h.ResolveSession(ctx, input)
	if err != nil {
		return err
	}
	if session == nil {
		return errors.New("sdk: session resolver returned nil session")
	}

	// Tool results enter through the same Entry point but are already the
	// output of a model/tool loop. Handle them before acquiring the session
	// turn lock because the parent turn already holds that lock.
	if input.Turn.Role == RoleToolResult || input.Turn.ToolResult != nil {
		session.Append(input.Turn)
		return nil
	}

	lockKey := input.SessionID
	if lockKey == "" {
		lockKey = session.ID()
	}
	if lockKey != "" {
		lock := h.sessionLock(lockKey)
		lock.Lock()
		defer lock.Unlock()
	}

	var req Request
	if h.BuildRequest != nil {
		req, err = h.BuildRequest(ctx, input, session)
		if err != nil {
			return err
		}
	}
	req.Messages = cloneTurns(session.History())
	req.Messages = append(req.Messages, cloneTurn(input.Turn))

	var responseTraced bool
	dispatchTrace := func(traceCtx context.Context, event TraceEvent) {
		switch event.Stage {
		case TraceProviderReady, TraceResponse, TraceResponseText, TraceResponseContent:
			responseTraced = true
		}
		traceCopy := event
		for _, display := range h.Displays {
			DispatchDisplay(traceCtx, display, Output{
				Source:    input.Source,
				SessionID: input.SessionID,
				Trace:     &traceCopy,
				Metadata:  cloneMetadata(input.Metadata),
			}, h.DisplayTimeout)
		}
	}

	ctx = WithSessionID(ctx, session.ID())
	var resp Response
	if h.Agent != nil {
		resp, err = h.Agent.RunTurnWithTraceAndEntry(ctx, session, input.Turn, req, dispatchTrace, h.Entry)
	} else {
		resp, err = h.Client.GenerateTurn(ctx, session, input.Turn, req)
	}
	if err != nil {
		return err
	}

	if responseTraced {
		return nil
	}

	output := Output{
		Source:    input.Source,
		SessionID: input.SessionID,
		Content:   append([]ContentPart(nil), resp.Content...),
		Response:  resp,
		Metadata:  cloneMetadata(input.Metadata),
	}
	for _, display := range h.Displays {
		DispatchDisplay(ctx, display, output, h.DisplayTimeout)
	}
	return nil
}

// Handle is kept as a compatibility alias for existing callers.
func (h *HarnessLoop) Handle(ctx context.Context, input Input) error {
	return h.Entry(ctx, input)
}

func (h *HarnessLoop) sessionLock(id string) *sync.Mutex {
	if existing, ok := h.sessionLocks.Load(id); ok {
		return existing.(*sync.Mutex)
	}
	created := &sync.Mutex{}
	actual, _ := h.sessionLocks.LoadOrStore(id, created)
	return actual.(*sync.Mutex)
}

func cloneMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func inputSourceForSession(sessionID string) string {
	if i := strings.IndexByte(sessionID, ':'); i > 0 {
		return sessionID[:i]
	}
	return ""
}
