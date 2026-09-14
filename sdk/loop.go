package sdk

import (
	"context"
	"errors"
	"log"
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
		h.Agent.SetSubAgentTraceSink(func(event SubAgentEvent) {
			if event.Parent == nil || event.Trace == nil {
				return
			}
			input := cloneInputRoute(event.Input)
			if input.SessionID == "" {
				input.SessionID = event.Parent.ID()
			}
			metadata := cloneMetadata(input.Metadata)
			if metadata == nil {
				metadata = map[string]string{}
			}
			metadata["trace_actor"] = "subagent"
			metadata["trace_job_id"] = event.JobID
			traceCopy := *event.Trace
			for _, display := range h.Displays {
				DispatchDisplay(context.WithoutCancel(ctx), display, Output{Source: input.Source, SessionID: input.SessionID, Trace: &traceCopy, Metadata: metadata}, h.DisplayTimeout)
			}
		})
		h.Agent.SetSubAgentEventSink(func(event SubAgentEvent) {
			if event.Parent == nil {
				return
			}
			input := cloneInputRoute(event.Input)
			if input.SessionID == "" {
				input.SessionID = event.Parent.ID()
			}
			input.Turn = Turn{Role: RoleUser, Content: []ContentPart{{Type: ContentText, Text: event.Message()}}}
			go func() {
				if err := h.Entry(context.WithoutCancel(ctx), input); err != nil {
					if h.OnTurnError != nil {
						h.OnTurnError(input, err)
					} else {
						log.Printf("sdk: sub-agent continuation failed source=%s session=%s: %v", input.Source, input.SessionID, err)
					}
				}
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

	if input.Turn.Role == RoleToolResult || input.Turn.ToolResult != nil {
		session.Append(input.Turn)
		return nil
	}

	lockKey := session.ID()
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
		metadata := cloneMetadata(input.Metadata)
		if metadata == nil {
			metadata = map[string]string{}
		}
		metadata["trace_actor"] = "main"
		for _, display := range h.Displays {
			DispatchDisplay(traceCtx, display, Output{Source: input.Source, SessionID: input.SessionID, Trace: &traceCopy, Metadata: metadata}, h.DisplayTimeout)
		}
	}

	ctx = WithSessionID(ctx, session.ID())
	ctx = context.WithValue(ctx, lifecycleInputKey{}, cloneInputRoute(input))
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

	output := Output{Source: input.Source, SessionID: input.SessionID, Content: append([]ContentPart(nil), resp.Content...), Response: resp, Metadata: cloneMetadata(input.Metadata)}
	if output.Metadata == nil {
		output.Metadata = map[string]string{}
	}
	output.Metadata["trace_actor"] = "main"
	for _, display := range h.Displays {
		DispatchDisplay(ctx, display, output, h.DisplayTimeout)
	}
	return nil
}

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

type lifecycleInputKey struct{}

func cloneInputRoute(input Input) Input {
	return Input{Source: input.Source, SessionID: input.SessionID, Metadata: cloneMetadata(input.Metadata)}
}
