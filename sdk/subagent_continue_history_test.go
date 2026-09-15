package sdk

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type toolUsingProvider struct {
	subAgentCaptureProvider
}

func (p *toolUsingProvider) WithAPIKey(string) Provider { return p }
func (p *toolUsingProvider) Generate(_ context.Context, req Request) (Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	if len(p.requests) == 1 {
		return Response{ToolCalls: []ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"path":"a.txt"}`}}}, nil
	}
	return Response{Content: []ContentPart{{Type: ContentText, Text: "worker done after tool"}}}, nil
}

func TestSubAgentHistoryShowsToolsArgsAndResults(t *testing.T) {
	r, events := orchestrationRunner(t, &toolUsingProvider{})
	id, err := r.Delegate(context.Background(), "use tools")
	if err != nil {
		t.Fatal(err)
	}
	awaitSubAgent(t, events)
	history := r.History(id)
	for _, want := range []string{"read_file", `{"path":"a.txt"}`, "tools_used:", "result: worker done after tool", "task: use tools"} {
		if !strings.Contains(history, want) {
			t.Fatalf("history missing %q:\n%s", want, history)
		}
	}
	status := r.Status(id)
	if !strings.Contains(status, "read_file") || !strings.Contains(status, "tools=") {
		t.Fatalf("status missing tool summary: %s", status)
	}
}

func TestSubAgentContinueReusesWorkerSession(t *testing.T) {
	provider := &subAgentCaptureProvider{}
	r, events := orchestrationRunner(t, provider)
	_ = r.parent.SetPlan([]string{"step one"})
	id, err := r.Delegate(context.Background(), "step one")
	if err != nil {
		t.Fatal(err)
	}
	awaitSubAgent(t, events)
	r.History(id)
	if err := r.Accept(id, "verified step one"); err != nil {
		t.Fatal(err)
	}
	// New work into the same worker session must be allowed after acceptance.
	next, err := r.Continue(context.Background(), id, "follow-on work in same session")
	if err != nil {
		t.Fatalf("continue rejected: %v", err)
	}
	if next == id {
		t.Fatal("continue reused job identity")
	}
	manager := r.manager
	manager.mu.RLock()
	first, ok1 := manager.jobs[id]
	second, ok2 := manager.jobs[next]
	manager.mu.RUnlock()
	if !ok1 || !ok2 {
		t.Fatal("jobs missing")
	}
	if first.workerID != second.workerID {
		t.Fatalf("worker session not reused: %q vs %q", first.workerID, second.workerID)
	}
	awaitSubAgent(t, events)
	req := provider.lastRequest()
	var history strings.Builder
	for _, turn := range req.Messages {
		for _, part := range turn.Content {
			history.WriteString(part.Text)
		}
	}
	if !strings.Contains(history.String(), "step one") || !strings.Contains(history.String(), "follow-on work in same session") {
		t.Fatalf("continued session lost history: %s", history.String())
	}
	// Follow-up on the accepted job must still be rejected; continue is the path.
	if _, err := r.FollowUp(context.Background(), id, "retry accepted"); err == nil {
		t.Fatal("follow-up on accepted job should fail")
	}
	// Cross-parent continue must be denied.
	other := &subAgentRunner{manager: r.manager, parent: NewSession(SessionConfig{ID: "other"}, nil)}
	if _, err := other.Continue(context.Background(), id, "steal"); err == nil {
		t.Fatal("cross-parent continue succeeded")
	}
	// Continue while running must be denied.
	blocking := &subAgentBlockingProvider{started: make(chan struct{})}
	rb, evts := orchestrationRunner(t, blocking)
	_ = rb.parent.SetPlan([]string{"long"})
	running, err := rb.Delegate(context.Background(), "long")
	if err != nil {
		t.Fatal(err)
	}
	<-blocking.started
	if _, err := rb.Continue(context.Background(), running, "overlap"); err == nil {
		t.Fatal("continue overlapped running job")
	}
	rb.Stop(running)
	awaitSubAgent(t, evts)
}

func TestSubAgentContinueToolValidation(t *testing.T) {
	r, events := orchestrationRunner(t, &subAgentImmediateProvider{})
	tool := newPlanningToolExecutor(nil, r.parent)
	tool.ConfigureSubAgent(r)
	if result := tool.Execute(context.Background(), ToolCall{Name: "continue_subagent", Arguments: `{"task":"missing id"}`}); !result.IsError {
		t.Fatal("continue without ID started work")
	}
	_ = r.parent.SetPlan([]string{"step"})
	id, err := r.Delegate(context.Background(), "step")
	if err != nil {
		t.Fatal(err)
	}
	awaitSubAgent(t, events)
	r.History(id)
	if err := r.Accept(id, "verified"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Acceptance has not happened yet; continue must wait for a terminal
			// job only in the sense of no running job — the first job is
			// terminal here so exactly one continue may win the reservation.
			if next, err := r.Continue(context.Background(), id, "extra"); err == nil {
				ids <- next
			}
		}()
	}
	wg.Wait()
	close(ids)
	var winners []string
	for next := range ids {
		winners = append(winners, next)
	}
	if len(winners) != 1 {
		t.Fatalf("continue reserved %d jobs", len(winners))
	}
	select {
	case <-events:
	case <-time.After(3 * time.Second):
		t.Fatal("missing continue completion event")
	}
}
