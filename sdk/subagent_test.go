package sdk

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type subAgentBlockingProvider struct{ started chan struct{} }

func (p *subAgentBlockingProvider) Name() string { return "test" }
func (p *subAgentBlockingProvider) Generate(ctx context.Context, _ Request) (Response, error) {
	select {
	case <-p.started:
	default:
		close(p.started)
	}
	<-ctx.Done()
	return Response{}, ctx.Err()
}
func (p *subAgentBlockingProvider) WithAPIKey(string) Provider { return p }

type subAgentImmediateProvider struct{}

func (p *subAgentImmediateProvider) Name() string { return "test" }
func (p *subAgentImmediateProvider) Generate(context.Context, Request) (Response, error) {
	return Response{Content: []ContentPart{{Type: ContentText, Text: "worker done"}}}, nil
}
func (p *subAgentImmediateProvider) WithAPIKey(string) Provider { return p }

type subAgentCaptureProvider struct {
	mu       sync.Mutex
	requests []Request
}

func (p *subAgentCaptureProvider) Name() string { return "test" }
func (p *subAgentCaptureProvider) Generate(_ context.Context, req Request) (Response, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	return Response{Content: []ContentPart{{Type: ContentText, Text: "worker done"}}}, nil
}
func (p *subAgentCaptureProvider) WithAPIKey(string) Provider { return p }
func (p *subAgentCaptureProvider) lastRequest() Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests[len(p.requests)-1]
}

func TestSubAgentInvestigationWorksBeforePlan(t *testing.T) {
	agent, parent := newSubAgentTest(t, &subAgentImmediateProvider{})
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	runner := &subAgentRunner{manager: manager, parent: parent}
	job, err := runner.Delegate(context.Background(), "read the repository requirements and summarize the current architecture")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && strings.Contains(runner.Status(job), "status=running") {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(runner.Status(job), "status=completed") {
		t.Fatalf("investigation did not complete: %s", runner.Status(job))
	}
	if len(parent.Plan().Steps) != 0 {
		t.Fatalf("investigation unexpectedly created plan: %#v", parent.Plan())
	}
}

func TestSubAgentReceivesFullPlanAndAdvancesOneStep(t *testing.T) {
	provider := &subAgentCaptureProvider{}
	agent, parent := newSubAgentTest(t, provider)
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	runner := &subAgentRunner{manager: manager, parent: parent}
	_ = parent.SetPlan([]string{"inspect and change A", "implement B", "verify C"})
	job, err := runner.Delegate(context.Background(), "inspect and change A")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && strings.Contains(runner.Status(job), "status=running") {
		time.Sleep(10 * time.Millisecond)
	}
	state := parent.Plan()
	if state.Current != 1 || state.Steps[0].Status != "completed" || state.Steps[1].Status != "ready" {
		t.Fatalf("unexpected plan state: %+v", state)
	}
	req := provider.lastRequest()
	if !strings.Contains(req.SystemPrompt, "1. inspect and change A") || !strings.Contains(req.SystemPrompt, "2. implement B") || !strings.Contains(req.SystemPrompt, "3. verify C") {
		t.Fatalf("full plan missing from worker system prompt: %s", req.SystemPrompt)
	}
}

func newSubAgentTest(t *testing.T, provider Provider) (*Agent, *Session) {
	t.Helper()
	r := NewRouter()
	keys := NewKeyPool("key")
	r.RegisterProvider(ProviderConfig{ID: "test", BaseURL: "http://test", Keys: keys, Adapter: AdapterOpenAI})
	r.Register(ModelRoute{Provider: "test", Model: "model", Adapter: AdapterOpenAI})
	client := NewRouterClient(r)
	client.RegisterAdapter(AdapterOpenAI, provider)
	db, err := OpenSessionDB(t.TempDir() + "/parent.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	session, err := OpenSession(dbPathForTest(db), SessionConfig{ID: "parent", Provider: "test", Model: "model"}, keys)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return &Agent{Client: client, SubAgentConfig: SubAgentConfig{Enabled: true}}, session
}

func dbPathForTest(db *SessionDB) string { return db.path }

func TestSubAgentDelegateReturnsImmediately(t *testing.T) {
	provider := &subAgentBlockingProvider{started: make(chan struct{})}
	agent, parent := newSubAgentTest(t, provider)
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	runner := &subAgentRunner{manager: manager, parent: parent}
	_ = parent.SetPlan([]string{"long task"})
	start := time.Now()
	job, err := runner.Delegate(context.Background(), "long task")
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("delegate blocked: %s", time.Since(start))
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	if !strings.Contains(runner.Status(job), "status=running") {
		t.Fatalf("unexpected status: %s", runner.Status(job))
	}
	if !runner.Stop(job) {
		t.Fatal("stop request was rejected")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !strings.Contains(runner.Status(job), "status=stopped") {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(runner.Status(job), "status=stopped") {
		t.Fatalf("worker did not stop: %s", runner.Status(job))
	}
}

func TestSubAgentCompletionPromptNamesID(t *testing.T) {
	event := SubAgentEvent{JobID: "sa-123", Status: "completed", Result: "worker done", PlanStep: PlanStep{Index: 1}}
	got := SubAgentCompletionPrompt(event)
	for _, want := range []string{"sub agent id sa-123", "finished", "subagent_history", "send_to_subagent"} {
		if !strings.Contains(got, want) {
			t.Fatalf("completion prompt missing %q: %s", want, got)
		}
	}
}

type subAgentTwoStepProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *subAgentTwoStepProvider) Name() string { return "test" }
func (p *subAgentTwoStepProvider) Generate(context.Context, Request) (Response, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.mu.Unlock()
	if n <= 1 {
		return Response{Content: []ContentPart{{Type: ContentText, Text: "partial result"}}}, nil
	}
	return Response{Content: []ContentPart{{Type: ContentText, Text: "final done"}}}, nil
}
func (p *subAgentTwoStepProvider) WithAPIKey(string) Provider { return p }

func TestSubAgentFollowUpContinuesSession(t *testing.T) {
	agent, parent := newSubAgentTest(t, &subAgentTwoStepProvider{})
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	eventCh := make(chan SubAgentEvent, 2)
	manager.SetEventSink(func(event SubAgentEvent) { eventCh <- event })
	runner := &subAgentRunner{manager: manager, parent: parent}
	job, err := runner.Delegate(context.Background(), "do the task")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-eventCh:
	case <-time.After(2 * time.Second):
		t.Fatal("first completion event was not emitted")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && strings.Contains(runner.Status(job), "status=running") {
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := runner.Send(job, "work is incomplete, finish it"); err != nil {
		t.Fatalf("follow-up send failed: %v", err)
	}
	select {
	case event := <-eventCh:
		if event.JobID != job || event.Status != "completed" {
			t.Fatalf("unexpected follow-up event: %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow-up completion event was not emitted")
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && strings.Contains(runner.Status(job), "status=running") {
		time.Sleep(10 * time.Millisecond)
	}
	history := runner.History(job)
	if !strings.Contains(history, "final done") {
		t.Fatalf("history missing worker final summary: %s", history)
	}
}

func TestSubAgentCompletionEmitsLifecycleEvent(t *testing.T) {
	agent, parent := newSubAgentTest(t, &subAgentImmediateProvider{})
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	eventCh := make(chan SubAgentEvent, 1)
	manager.SetEventSink(func(event SubAgentEvent) { eventCh <- event })
	runner := &subAgentRunner{manager: manager, parent: parent}
	_ = parent.SetPlan([]string{"quick task"})
	job, err := runner.Delegate(context.Background(), "quick task")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-eventCh:
		if event.JobID != job || event.Status != "completed" || event.PlanStep.Index != 1 {
			t.Fatalf("unexpected event: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("sub-agent lifecycle event was not emitted")
	}
}

func TestSubAgentUsesSeparateSession(t *testing.T) {
	agent, parent := newSubAgentTest(t, &subAgentImmediateProvider{})
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	runner := &subAgentRunner{manager: manager, parent: parent}
	_ = parent.SetPlan([]string{"isolated task"})
	job, err := runner.Delegate(context.Background(), "isolated task")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && strings.Contains(runner.Status(job), "status=running") {
		time.Sleep(10 * time.Millisecond)
	}
	if len(parent.History()) != 0 {
		t.Fatalf("worker changed parent history: %#v", parent.History())
	}
}
