package sdk

import (
	"context"
	"strings"
	"sync"
	"testing"
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

func TestSubAgentReportShowsToolsArgsAndResults(t *testing.T) {
	agent, parent := newSubAgentTest(t, &toolUsingProvider{})
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	r := &subAgentRunner{manager: manager, parent: parent}
	report, err := r.Delegate(context.Background(), "use tools")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"read_file", "a.txt", "tools_used:", "worker done after tool", "task: use tools"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
	if !strings.Contains(report, "result:") {
		t.Fatalf("report missing final result:\n%s", report)
	}
}

func TestSubAgentContinueReusesWorkerSession(t *testing.T) {
	provider := &subAgentCaptureProvider{}
	agent, parent := newSubAgentTest(t, provider)
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	r := &subAgentRunner{manager: manager, parent: parent}
	_ = parent.SetPlan([]string{"step one"})
	report, err := r.Delegate(context.Background(), "step one")
	if err != nil {
		t.Fatal(err)
	}
	id := jobIDFromReport(report)
	if err := r.Accept(id, "verified step one"); err != nil {
		t.Fatal(err)
	}
	// New work into the same worker session must be allowed after acceptance.
	nextReport, err := r.Continue(context.Background(), id, "follow-on work in same session")
	if err != nil {
		t.Fatalf("continue rejected: %v", err)
	}
	next := jobIDFromReport(nextReport)
	if next == id {
		t.Fatal("continue reused job identity")
	}
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
	other := &subAgentRunner{manager: manager, parent: NewSession(SessionConfig{ID: "other"}, nil)}
	if _, err := other.Continue(context.Background(), id, "steal"); err == nil {
		t.Fatal("cross-parent continue succeeded")
	}
	// Continue while running must be denied.
	blocking := &subAgentBlockingProvider{started: make(chan struct{})}
	agentB, parentB := newSubAgentTest(t, blocking)
	managerB := newSubAgentManager(agentB, agentB.SubAgentConfig)
	rb := &subAgentRunner{manager: managerB, parent: parentB}
	_ = parentB.SetPlan([]string{"long"})
	running, err := managerB.startAndRegister(parentB, "long")
	if err != nil {
		t.Fatal(err)
	}
	<-blocking.started
	if _, err := rb.Continue(context.Background(), running, "overlap"); err == nil {
		t.Fatal("continue overlapped running job")
	}
	if _, err := rb.Stop(context.Background(), running); err != nil {
		t.Fatal(err)
	}
}

func TestSubAgentContinueToolValidation(t *testing.T) {
	agent, parent := newSubAgentTest(t, &subAgentImmediateProvider{})
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	r := &subAgentRunner{manager: manager, parent: parent}
	tool := newPlanningToolExecutor(nil, parent)
	tool.ConfigureSubAgent(r)
	if result := tool.Execute(context.Background(), ToolCall{Name: "continue_subagent", Arguments: `{"task":"missing id"}`}); !result.IsError {
		t.Fatal("continue without ID started work")
	}
	_ = parent.SetPlan([]string{"step"})
	report, err := r.Delegate(context.Background(), "step")
	if err != nil {
		t.Fatal(err)
	}
	id := jobIDFromReport(report)
	if err := r.Accept(id, "verified"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := 0
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Continue(context.Background(), id, "extra"); err == nil {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("continue reserved %d jobs", winners)
	}
}
