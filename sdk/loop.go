package sdk

import (
	"context"
	"errors"
	"sync"
	"time"
)

type SessionResolver func(context.Context, Input) (*Session, error)
type RequestResolver func(context.Context, Input, *Session) (Request, error)

type HarnessLoop struct {
	Client          *RouterClient
	Agent           *Agent
	Source          InputSource
	ResolveSession  SessionResolver
	BuildRequest    RequestResolver
	Displays        []Display
	DisplayTimeout  time.Duration
	OnTurnError     func(Input, error)

	sessionLocks sync.Map
}

func (h *HarnessLoop) Run(ctx context.Context) error {
	if h == nil || (h.Client == nil && h.Agent == nil) || h.Source == nil || h.ResolveSession == nil {
		return errors.New("sdk: incomplete harness loop configuration")
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
			if err := h.Handle(ctx, input); err != nil && h.OnTurnError != nil {
				h.OnTurnError(input, err)
			}
		}
	}
}

func (h *HarnessLoop) Handle(ctx context.Context, input Input) error {
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

	var responseTraced bool
	dispatchTrace := func(traceCtx context.Context, event TraceEvent) {
		if event.Stage == TraceResponse || event.Stage == TraceResponseText {
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

	var resp Response
	if h.Agent != nil {
		resp, err = h.Agent.RunTurnWithTrace(ctx, session, input.Turn, req, dispatchTrace)
	} else {
		resp, err = h.Client.GenerateTurn(ctx, session, input.Turn, req)
	}
	if err != nil {
		return err
	}

	// Agent responses are already displayed through TraceResponse or streamed
	// TraceResponseText events. Sending the final output as well would duplicate it.
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
