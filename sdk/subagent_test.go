package sdk

import (
	"context"
	"errors"
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
func (p *subAgentBlockingProvider) Stream(context.Context, Request) (<-chan Event, error) {
	return nil, errors.New("not implemented")
}
func (p *subAgentBlockingProvider) WithAPIKey(string) Provider { return p }

type subAgentImmediateProvider struct{}

func (p *subAgentImmediateProvider) Name() string { return "test" }
func (p *subAgentImmediateProvider) Generate(context.Context, Request) (Response, error) {
	return Response{Content: []ContentPart{{Type: ContentText, Text: "worker done"}}}, nil
}
func (p *subAgentImmediateProvider) Stream(context.Context, Request) (<-chan Event, error) {
	return nil, errors.New("not implemented")
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
func (p *subAgentCaptureProvider) Stream(context.Context, Request) (<-chan Event, error) {
	return nil, errors.New("not implemented")
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

func TestSubAgentReceivesFullPlanAndRequiresAcceptance(t *testing.T) {
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
	if state.Current != 0 || state.Steps[0].Status != "awaiting_review" || state.Steps[1].Status != "pending" {
		t.Fatalf("worker must await review: %+v", state)
	}
	_ = runner.History(job)
	if err := runner.Accept(job, "Reviewed worker changes and validation"); err != nil {
		t.Fatal(err)
	}
	state = parent.Plan()
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
