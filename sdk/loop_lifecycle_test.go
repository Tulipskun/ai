package sdk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type lifecycleProvider struct {
	mu        sync.Mutex
	delegated bool
}

func (p *lifecycleProvider) Name() string               { return "test" }
func (p *lifecycleProvider) WithAPIKey(string) Provider { return p }
func (p *lifecycleProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	resp, err := p.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	ch := make(chan Event, len(resp.Content)+len(resp.ToolCalls)+1)
	for _, part := range resp.Content {
		ch <- Event{Type: EventText, Text: part.Text}
	}
	for _, call := range resp.ToolCalls {
		call := call
		ch <- Event{Type: EventToolCall, ToolCall: &call}
	}
	ch <- Event{Type: EventDone}
	close(ch)
	return ch, nil
}
func (p *lifecycleProvider) Generate(_ context.Context, req Request) (Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.Contains(req.SystemPrompt, "You are the worker sub-agent") {
		return Response{Content: []ContentPart{{Type: ContentText, Text: "worker findings"}}}, nil
	}
	if !p.delegated {
		p.delegated = true
		return Response{ToolCalls: []ToolCall{{ID: "delegate", Name: "delegate_to_subagent", Arguments: `{"task":"investigate"}`}}}, nil
	}
	for _, turn := range req.Messages {
		for _, part := range turn.Content {
			if strings.Contains(part.Text, "<sub agent id ") {
				return Response{Content: []ContentPart{{Type: ContentText, Text: "reviewed lifecycle result"}}}, nil
			}
		}
	}
	return Response{Content: []ContentPart{{Type: ContentText, Text: "waiting for event"}}}, nil
}

func TestLoopLifecyclePreservesCanonicalRouteAndReportsErrors(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%v/%s", stream, map[bool]string{false: "mapped-discord", true: "continuation-error"}[fail]), func(t *testing.T) {
				agent, parent := newSubAgentTest(t, &lifecycleProvider{})
				input := Input{Source: "discord", SessionID: "mapped-project-session", Metadata: map[string]string{"channel_id": "channel-123", "original": "preserve"}, Turn: Turn{Role: RoleUser, Content: []ContentPart{{Type: ContentText, Text: "new task"}}}}
				outputs := make(chan Output, 64)
				continuations := make(chan Input, 2)
				turnErrors := make(chan Input, 2)
				expectedErr := errors.New("continuation request failed")
				loop := &HarnessLoop{Agent: agent, Source: staticInputSource{inputs: []Input{input}}, ResolveSession: func(_ context.Context, in Input) (*Session, error) {
					if in.Source != "tool" && in.SessionID != "mapped-project-session" {
						return nil, errors.New("lost mapped routing alias")
					}
					return parent, nil
				}, BuildRequest: func(_ context.Context, in Input, _ *Session) (Request, error) {
					if len(in.Turn.Content) > 0 && strings.Contains(in.Turn.Content[0].Text, "<sub agent id ") {
						continuations <- in
						if fail {
							return Request{}, expectedErr
						}
					}
					return Request{Stream: stream}, nil
				}, Displays: []Display{DisplayFunc(func(_ context.Context, out Output) error { outputs <- out; return nil })}, OnTurnError: func(in Input, err error) {
					if !errors.Is(err, expectedErr) {
						t.Errorf("unexpected error: %v", err)
					}
					turnErrors <- in
				}}
				if err := loop.Run(context.Background()); err != nil {
					t.Fatal(err)
				}
				input.Metadata["channel_id"] = "mutated"
				select {
				case in := <-continuations:
					if in.Source != "discord" || in.SessionID != "mapped-project-session" || in.Metadata["channel_id"] != "channel-123" || in.Metadata["original"] != "preserve" {
						t.Fatalf("lost lifecycle route: %+v", in)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("missing lifecycle continuation")
				}
				if fail {
					select {
					case in := <-turnErrors:
						if in.Metadata["channel_id"] != "channel-123" {
							t.Fatalf("lost error route: %+v", in)
						}
					case <-time.After(3 * time.Second):
						t.Fatal("continuation error silently discarded")
					}
					return
				}
				deadline := time.After(3 * time.Second)
				for {
					select {
					case out := <-outputs:
						if out.Trace == nil || out.Trace.Stage != TraceResponse || out.Trace.Response == nil {
							continue
						}
						if responseText(*out.Trace.Response) != "reviewed lifecycle result" {
							continue
						}
						if out.Source != "discord" || out.SessionID != "mapped-project-session" || out.Metadata["channel_id"] != "channel-123" {
							t.Fatalf("misrouted lifecycle output: %+v", out)
						}
						return
					case <-deadline:
						t.Fatal("missing lifecycle response")
					}
				}
			})
		}
	}
}

func TestSubAgentFollowUpPreservesOriginalLifecycleMetadata(t *testing.T) {
	r, events := orchestrationRunner(t, &subAgentImmediateProvider{})
	input := Input{Source: "custom-transport", SessionID: "opaque-alias", Metadata: map[string]string{"channel_id": "original"}}
	ctx := context.WithValue(context.Background(), lifecycleInputKey{}, input)
	id, err := r.Delegate(ctx, "investigate")
	if err != nil {
		t.Fatal(err)
	}
	input.Metadata["channel_id"] = "changed"
	event := awaitSubAgent(t, events)
	if event.Input.Source != "custom-transport" || event.Input.Metadata["channel_id"] != "original" {
		t.Fatalf("route not captured: %+v", event.Input)
	}
	event.Input.Metadata["channel_id"] = "event-mutated"
	ctx = context.WithValue(context.Background(), lifecycleInputKey{}, Input{Source: "wrong", Metadata: map[string]string{"channel_id": "wrong"}})
	_, err = r.FollowUp(ctx, id, "clarify findings")
	if err != nil {
		t.Fatal(err)
	}
	retry := awaitSubAgent(t, events)
	if retry.Input.Source != "custom-transport" || retry.Input.SessionID != "opaque-alias" || retry.Input.Metadata["channel_id"] != "original" {
		t.Fatalf("follow-up lost original route: %+v", retry.Input)
	}
}
