package sdk

import (
	"context"
	"testing"
)

type streamDoneToolProvider struct{}

func (streamDoneToolProvider) Name() string { return "stream-done-tool" }
func (streamDoneToolProvider) Generate(context.Context, Request) (Response, error) {
	return Response{}, nil
}
func (streamDoneToolProvider) Stream(context.Context, Request) (<-chan Event, error) {
	ch := make(chan Event, 1)
	ch <- Event{Type: EventDone, Response: &Response{
		ToolCalls: []ToolCall{{ID: "call-1", Name: "echo", Arguments: `{}`}},
	}}
	close(ch)
	return ch, nil
}
func (streamDoneToolProvider) WithAPIKey(string) Provider { return streamDoneToolProvider{} }

func TestAgentStreamingTraceEmitsToolCallFromCompletedResponse(t *testing.T) {
	provider := streamDoneToolProvider{}
	client, session := newAgentTestSession(provider)
	tools := &agentTestTools{definitions: []Tool{{Name: "echo"}}}
	agent := &Agent{Client: client, Tools: tools, MaxRetries: 0, DisablePlanning: true}

	var toolCalls []string
	_, err := agent.RunTurnWithTrace(context.Background(), session, Turn{Role: RoleUser}, Request{Stream: true}, func(_ context.Context, event TraceEvent) {
		if event.Stage == TraceToolCall && event.ToolCall != nil {
			toolCalls = append(toolCalls, event.ToolCall.Name)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(toolCalls) != 1 || toolCalls[0] != "echo" {
		t.Fatalf("tool trace = %v", toolCalls)
	}
	if len(tools.results) != 1 || tools.results[0].ID != "call-1" {
		t.Fatalf("tool execution = %#v", tools.results)
	}
}

type streamEventAndDoneToolProvider struct{}

func (streamEventAndDoneToolProvider) Name() string { return "stream-event-and-done-tool" }
func (streamEventAndDoneToolProvider) Generate(context.Context, Request) (Response, error) {
	return Response{}, nil
}
func (streamEventAndDoneToolProvider) Stream(context.Context, Request) (<-chan Event, error) {
	ch := make(chan Event, 2)
	call := ToolCall{ID: "call-1", Name: "echo", Arguments: `{}`}
	ch <- Event{Type: EventToolCall, ToolCall: &call}
	ch <- Event{Type: EventDone, Response: &Response{ToolCalls: []ToolCall{call}}}
	close(ch)
	return ch, nil
}
func (streamEventAndDoneToolProvider) WithAPIKey(string) Provider { return streamEventAndDoneToolProvider{} }

func TestAgentStreamingTraceDoesNotDuplicateToolCallFromCompletedResponse(t *testing.T) {
	provider := streamEventAndDoneToolProvider{}
	client, session := newAgentTestSession(provider)
	tools := &agentTestTools{definitions: []Tool{{Name: "echo"}}}
	agent := &Agent{Client: client, Tools: tools, MaxRetries: 0, DisablePlanning: true}

	count := 0
	_, err := agent.RunTurnWithTrace(context.Background(), session, Turn{Role: RoleUser}, Request{Stream: true}, func(_ context.Context, event TraceEvent) {
		if event.Stage == TraceToolCall && event.ToolCall != nil && event.ToolCall.Name == "echo" {
			count++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("tool trace count = %d, want 1", count)
	}
}
