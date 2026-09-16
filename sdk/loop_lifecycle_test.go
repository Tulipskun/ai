package sdk

import (
	"context"
	"errors"
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
	ch <- Event{Type: EventDone, Response: &resp}
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
		if turn.Role == RoleToolResult && turn.ToolResult != nil && strings.Contains(turn.ToolResult.Content, "worker findings") {
			return Response{Content: []ContentPart{{Type: ContentText, Text: "reviewed lifecycle result"}}}, nil
		}
	}
	return Response{Content: []ContentPart{{Type: ContentText, Text: "missing report"}}}, nil
}

func TestLoopDeliversWorkerReportInsideOneMainTurn(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(boolLabel("stream", stream), func(t *testing.T) {
			agent, parent := newSubAgentTest(t, &lifecycleProvider{})
			input := Input{Source: "discord", SessionID: "mapped-project-session", Metadata: map[string]string{"channel_id": "channel-123", "original": "preserve"}, Turn: Turn{Role: RoleUser, Content: []ContentPart{{Type: ContentText, Text: "new task"}}}}
			var mu sync.Mutex
			mainTurns := 0
			var toolResults []string
			continuations := 0
			tracedWorkerStages := map[TraceStage]bool{}
			loop := &HarnessLoop{Agent: agent, Source: staticInputSource{inputs: []Input{input}}, ResolveSession: func(_ context.Context, in Input) (*Session, error) {
				if in.Source != "tool" && in.SessionID != "mapped-project-session" {
					return nil, errors.New("lost mapped routing alias")
				}
				return parent, nil
			}, BuildRequest: func(_ context.Context, in Input, _ *Session) (Request, error) {
				mu.Lock()
				if in.Source == "discord" {
					mainTurns++
					if in.Metadata["channel_id"] != "channel-123" || in.Metadata["original"] != "preserve" {
						t.Errorf("lost lifecycle metadata on continuation: %+v", in.Metadata)
					}
				} else {
					continuations++
				}
				mu.Unlock()
				return Request{Stream: stream}, nil
			}, Displays: []Display{DisplayFunc(func(_ context.Context, out Output) error {
				mu.Lock()
				defer mu.Unlock()
				if out.Trace != nil && out.Metadata["trace_actor"] == "subagent" {
					tracedWorkerStages[out.Trace.Stage] = true
					if out.Metadata["channel_id"] != "channel-123" {
						t.Errorf("worker trace lost channel: %+v", out.Metadata)
					}
				}
				if out.Trace != nil && out.Trace.Stage == TraceToolResult && out.Trace.ToolResult != nil {
					toolResults = append(toolResults, out.Trace.ToolResult.Content)
				}
				return nil
			})}}
			if err := loop.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if mainTurns != 1 {
				t.Fatalf("blocking orchestration must run inside one main turn, got %d", mainTurns)
			}
			if continuations != 0 {
				t.Fatalf("completion events must not inject extra turns (REQ-021), got %d", continuations)
			}
			var reports int
			for _, content := range toolResults {
				if strings.Contains(content, "worker findings") && strings.Contains(content, "status=completed") {
					reports++
				}
			}
			if reports == 0 {
				t.Fatalf("delegate tool result missing the worker report: %v", toolResults)
			}
			if !tracedWorkerStages[TraceToolCall] && !tracedWorkerStages[TraceResponse] {
				t.Fatalf("worker trace events did not reach displays: %+v", tracedWorkerStages)
			}
		})
	}
}

func boolLabel(kind string, v bool) string {
	if v {
		return kind + "=true"
	}
	return kind + "=false"
}

func TestSubAgentFollowUpPreservesOriginalRouteMetadata(t *testing.T) {
	agent, parent := newSubAgentTest(t, &subAgentImmediateProvider{})
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	r := &subAgentRunner{manager: manager, parent: parent}
	input := Input{Source: "custom-transport", SessionID: "opaque-alias", Metadata: map[string]string{"channel_id": "original"}}
	ctx := context.WithValue(context.Background(), lifecycleInputKey{}, input)
	traces := make(chan SubAgentEvent, 8)
	manager.SetTraceSink(func(event SubAgentEvent) { traces <- event })
	report, err := r.Delegate(ctx, "investigate")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	captured := false
	for !captured {
		select {
		case event := <-traces:
			captured = event.Input.Source == "custom-transport" && event.Input.Metadata["channel_id"] == "original"
		case <-deadline:
			t.Fatal("worker trace never carried the original route")
		}
	}
	input.Metadata["channel_id"] = "changed"
	retryReport, err := r.FollowUp(context.Background(), jobIDFromReport(report), "clarify findings")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(retryReport, "worker done") {
		t.Fatalf("follow-up lost report: %s", retryReport)
	}
	for _, event := range drainTraces(traces) {
		if event.Input.Source != "custom-transport" || event.Input.SessionID != "opaque-alias" || event.Input.Metadata["channel_id"] != "original" {
			t.Fatalf("follow-up lost original route: %+v", event.Input)
		}
	}
}

func drainTraces(ch chan SubAgentEvent) []SubAgentEvent {
	var events []SubAgentEvent
	for {
		select {
		case event := <-ch:
			events = append(events, event)
		default:
			return events
		}
	}
}
