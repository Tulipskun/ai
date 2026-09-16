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
	report, err := runner.Delegate(context.Background(), "read the repository requirements and summarize the current architecture")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "status=completed") {
		t.Fatalf("investigation did not complete: %s", report)
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
	report, err := runner.Delegate(context.Background(), "inspect and change A")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "worker done") || !strings.Contains(report, "status=completed") {
		t.Fatalf("blocking call lost the handoff report: %s", report)
	}
	state := parent.Plan()
	if state.Current != 0 || state.Steps[0].Status != "awaiting_review" || state.Steps[1].Status != "pending" {
		t.Fatalf("worker must await review: %+v", state)
	}
	if err := runner.Accept(jobIDFromReport(report), "Reviewed worker changes and validation"); err != nil {
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

func jobIDFromReport(report string) string {
	for _, field := range strings.Fields(report) {
		if strings.HasPrefix(field, "sa-") {
			return strings.Trim(field, ":")
		}
	}
	return ""
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

func TestSubAgentDelegateBlocksUntilTerminal(t *testing.T) {
	provider := &subAgentBlockingProvider{started: make(chan struct{})}
	agent, parent := newSubAgentTest(t, provider)
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	runner := &subAgentRunner{manager: manager, parent: parent}
	_ = parent.SetPlan([]string{"long task"})
	returned := make(chan string, 1)
	go func() {
		report, err := runner.Delegate(context.Background(), "long task")
		if err != nil {
			returned <- "error: " + err.Error()
			return
		}
		returned <- report
	}()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	select {
	case report := <-returned:
		t.Fatalf("delegate returned before the worker finished: %s", report)
	case <-time.After(150 * time.Millisecond):
	}
	// Stopping unblocks the waiting delegate with the final report (REQ-020).
	id := manager.runningJobID()
	if id == "" {
		t.Fatal("running job not registered")
	}
	stopReport, err := runner.Stop(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stopReport, "status=stopped") {
		t.Fatalf("stop did not wait for the terminal state: %s", stopReport)
	}
	select {
	case report := <-returned:
		if !strings.Contains(report, "status=stopped") {
			t.Fatalf("delegate lost the stopped report: %s", report)
		}
	case <-time.After(time.Second):
		t.Fatal("delegate did not return after the job stopped")
	}
}

func TestSubAgentUsesSeparateSession(t *testing.T) {
	agent, parent := newSubAgentTest(t, &subAgentImmediateProvider{})
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	runner := &subAgentRunner{manager: manager, parent: parent}
	_ = parent.SetPlan([]string{"isolated task"})
	if _, err := runner.Delegate(context.Background(), "isolated task"); err != nil {
		t.Fatal(err)
	}
	if len(parent.History()) != 0 {
		t.Fatalf("worker changed parent history: %#v", parent.History())
	}
}

func TestSubAgentRunnerHasNoPollingTools(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range (&subAgentTool{}).Definitions() {
		names[tool.Name] = true
	}
	for gone := range map[string]bool{"subagent_status": true, "subagent_history": true} {
		if names[gone] {
			t.Fatalf("polling tool %q must not be exposed (REQ-034)", gone)
		}
	}
	for _, want := range []string{"delegate_to_subagent", "follow_up_subagent", "continue_subagent", "stop_subagent", "accept_subagent_result"} {
		if !names[want] {
			t.Fatalf("missing orchestration tool %q", want)
		}
	}
}

func TestSubAgentStopWaitsForStoppedWorker(t *testing.T) {
	provider := &subAgentBlockingProvider{started: make(chan struct{})}
	agent, parent := newSubAgentTest(t, provider)
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	runner := &subAgentRunner{manager: manager, parent: parent}
	id, err := manager.startAndRegister(parent, "never finishing")
	if err != nil {
		t.Fatal(err)
	}
	<-provider.started
	report, err := runner.Stop(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "status=stopped") {
		t.Fatalf("stop returned before the worker stopped: %s", report)
	}
	if !strings.Contains(report, "task: never finishing") {
		t.Fatalf("stop report lost the task: %s", report)
	}
	if _, err := runner.Stop(context.Background(), id); err == nil {
		t.Fatal("stop accepted for an already stopped job")
	}
}
