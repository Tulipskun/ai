package sdk

import "testing"

func TestSubAgentSinksSeparateTraceFromCompletion(t *testing.T) {
	manager := newSubAgentManager(nil, SubAgentConfig{})
	traceCh := make(chan SubAgentEvent, 1)
	completionCh := make(chan SubAgentEvent, 1)
	manager.SetTraceSink(func(event SubAgentEvent) { traceCh <- event })
	manager.SetEventSink(func(event SubAgentEvent) { completionCh <- event })

	manager.emitTrace(&subAgentJob{id: "sa-test"}, TraceEvent{Stage: TraceToolCall})
	select {
	case event := <-traceCh:
		if event.Trace == nil || event.Trace.Stage != TraceToolCall {
			t.Fatalf("unexpected trace event: %+v", event)
		}
	default:
		t.Fatal("trace sink did not receive live event")
	}
	select {
	case event := <-completionCh:
		t.Fatalf("completion sink received live trace: %+v", event)
	default:
	}
}
