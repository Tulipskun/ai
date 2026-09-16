package sdk

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type retryResultProvider struct {
	subAgentCaptureProvider
	fail bool
}

func (p *retryResultProvider) WithAPIKey(string) Provider { return p }
func (p *retryResultProvider) Generate(_ context.Context, req Request) (Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	if len(p.requests) == 1 {
		if p.fail {
			return Response{}, errors.New("worker failed")
		}
		return Response{Content: []ContentPart{{Type: ContentText, Text: "BLOCKED: incomplete; tests not run"}}}, nil
	}
	return Response{Content: []ContentPart{{Type: ContentText, Text: "Implemented and tests passed"}}}, nil
}

func TestSubAgentBlockedResultRetryAndAcceptance(t *testing.T) {
	provider := &retryResultProvider{}
	agent, parent := newSubAgentTest(t, provider)
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	r := &subAgentRunner{manager: manager, parent: parent}
	_ = parent.SetPlan([]string{"implement", "validate"})
	report, err := r.Delegate(context.Background(), "implement")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "BLOCKED") {
		t.Fatalf("blocked report missing from delegation result: %s", report)
	}
	state := parent.Plan()
	if state.Current != 0 || state.Steps[0].Status != "awaiting_review" {
		t.Fatalf("blocked report advanced plan: %+v", state)
	}
	id := jobIDFromReport(report)
	if _, err := r.Delegate(context.Background(), "skip to validation"); err == nil {
		t.Fatal("delegated without acceptance")
	}
	retryReport, err := r.FollowUp(context.Background(), id, "Resolve block and run tests")
	if err != nil {
		t.Fatal(err)
	}
	retry := jobIDFromReport(retryReport)
	if retry == id {
		t.Fatal("retry reused result identity")
	}
	if !strings.Contains(retryReport, "Implemented and tests passed") {
		t.Fatalf("retry lost its report: %s", retryReport)
	}
	req := provider.lastRequest()
	var history strings.Builder
	for _, turn := range req.Messages {
		for _, part := range turn.Content {
			history.WriteString(part.Text)
		}
	}
	if !strings.Contains(history.String(), "BLOCKED") || !strings.Contains(history.String(), "Resolve block") {
		t.Fatalf("retry lost worker history: %s", history.String())
	}
	if err := r.Accept(id, "old result"); err == nil {
		t.Fatal("accepted superseded attempt")
	}
	if _, err := r.FollowUp(context.Background(), id, "again"); err == nil {
		t.Fatal("retried superseded attempt")
	}
	if err := r.Accept(retry, ""); err == nil {
		t.Fatal("accepted without verification")
	}
	if err := r.Accept(retry, "Reviewed implementation and passing test report"); err != nil {
		t.Fatal(err)
	}
	if err := r.Accept(retry, "twice"); err == nil {
		t.Fatal("accepted result twice")
	}
	if _, err := r.FollowUp(context.Background(), retry, "again"); err == nil {
		t.Fatal("retried accepted result")
	}
	state = parent.Plan()
	if state.Current != 1 || state.Steps[1].Status != "ready" {
		t.Fatalf("accept did not advance exactly once: %+v", state)
	}
}

func TestSubAgentFailureRemainsRetryable(t *testing.T) {
	agent, parent := newSubAgentTest(t, &retryResultProvider{fail: true})
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	manager.agent.MaxRetries = -1
	r := &subAgentRunner{manager: manager, parent: parent}
	_ = parent.SetPlan([]string{"fix"})
	report, err := r.Delegate(context.Background(), "fix")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "status=failed") {
		t.Fatalf("expected failed report: %s", report)
	}
	id := jobIDFromReport(report)
	if err := r.Accept(id, "failed"); err == nil {
		t.Fatal("accepted failed execution")
	}
	if state := parent.Plan(); state.Current != 0 || state.Steps[0].Status != "failed" {
		t.Fatalf("bad failure state: %+v", state)
	}
	retryReport, err := r.FollowUp(context.Background(), id, "retry fix")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(retryReport, "status=completed") {
		t.Fatalf("retry did not complete: %s", retryReport)
	}
	if err := r.Accept(jobIDFromReport(retryReport), "verified retry"); err != nil {
		t.Fatal(err)
	}
}

func TestSubAgentReservationAndStaleCompletion(t *testing.T) {
	for _, planned := range []bool{false, true} {
		t.Run(map[bool]string{false: "investigation", true: "planned"}[planned], func(t *testing.T) {
			provider := &subAgentBlockingProvider{started: make(chan struct{})}
			agent, parent := newSubAgentTest(t, provider)
			manager := newSubAgentManager(agent, agent.SubAgentConfig)
			r := &subAgentRunner{manager: manager, parent: parent}
			if planned {
				_ = parent.SetPlan([]string{"old"})
			}
			id, err := manager.startAndRegister(parent, "work")
			if err != nil {
				t.Fatal(err)
			}
			<-provider.started
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, err := r.Delegate(context.Background(), "overlap"); err == nil {
						t.Error("reservation allowed overlap")
					}
					if _, err := r.FollowUp(context.Background(), id, "overlap retry"); err == nil {
						t.Error("running job retried")
					}
				}()
			}
			wg.Wait()
			oldRevision := parent.Plan().Revision
			_ = parent.SetPlan([]string{"replacement"})
			if parent.Plan().Revision == oldRevision {
				t.Fatal("replacement reused revision")
			}
			report, err := r.Stop(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(report, "status=stopped") {
				t.Fatalf("stop report wrong: %s", report)
			}
			if state := parent.Plan(); len(state.Steps) != 1 || state.Steps[0].Status != "ready" {
				t.Fatalf("stale completion mutated replacement: %+v", state)
			}
			if err := r.Accept(id, "stale"); err == nil {
				t.Fatal("accepted stale result")
			}
			if _, err := r.FollowUp(context.Background(), id, "stale"); err == nil {
				t.Fatal("retried stale result")
			}
		})
	}
}

func TestSubAgentStaleSuccessfulResultAndCompletedPlanInvestigation(t *testing.T) {
	agent, parent := newSubAgentTest(t, &subAgentImmediateProvider{})
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	r := &subAgentRunner{manager: manager, parent: parent}
	_ = parent.SetPlan([]string{"same text"})
	id, err := r.Delegate(context.Background(), "same text")
	if err != nil {
		t.Fatal(err)
	}
	_ = parent.SetPlan([]string{"same text"})
	if err := r.Accept(jobIDFromReport(id), "stale identical step"); err == nil {
		t.Fatal("accepted old revision")
	}
	id, err = r.Delegate(context.Background(), "same text")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Accept(jobIDFromReport(id), "verified"); err != nil {
		t.Fatal(err)
	}
	revision := parent.Plan().Revision
	report, err := r.Delegate(context.Background(), "investigate new task")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "investigation") {
		t.Fatalf("not investigation: %s", report)
	}
	if state := parent.Plan(); state.Revision != revision || state.Current != 1 || state.Steps[0].Status != "completed" {
		t.Fatalf("investigation reset completed plan: %+v", state)
	}
}

func TestSubAgentCrossParentOperationsDenied(t *testing.T) {
	provider := &subAgentBlockingProvider{started: make(chan struct{})}
	agent, parent := newSubAgentTest(t, provider)
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	r := &subAgentRunner{manager: manager, parent: parent}
	other := &subAgentRunner{manager: manager, parent: NewSession(SessionConfig{ID: "other"}, nil)}
	_ = parent.SetPlan([]string{"secret"})
	id, err := manager.startAndRegister(parent, "secret")
	if err != nil {
		t.Fatal(err)
	}
	<-provider.started
	if _, err := other.Stop(context.Background(), id); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("cross-parent stop: %v", err)
	}
	if _, err := other.FollowUp(context.Background(), id, "steal"); err == nil {
		t.Fatal("cross-parent follow-up succeeded")
	}
	if err := other.Accept(id, "steal"); err == nil {
		t.Fatal("cross-parent acceptance succeeded")
	}
	if strings.Contains(other.Status(id), "secret") {
		t.Fatal("cross-parent status leaked the task")
	}
	if _, err := r.Stop(context.Background(), id); err != nil {
		t.Fatal(err)
	}
}

func TestPlanningGuidanceDescribesBlockingOrchestration(t *testing.T) {
	for _, want := range []string{"You hold the project overview and own the checklist", "Delegation is blocking", "complete handoff report", "NOT verified success", "follow_up_subagent", "continue_subagent", "accept_subagent_result", "`stop_subagent` blocks until the worker has actually stopped"} {
		if !strings.Contains(planningSystemInstruction, want) {
			t.Fatalf("missing guidance %q", want)
		}
	}
	if strings.Contains(planningSystemInstruction, "subagent_status") || strings.Contains(planningSystemInstruction, "subagent_history") {
		t.Fatal("removed polling tools still referenced")
	}
}

type releasedSubAgentProvider struct {
	subAgentImmediateProvider
	started chan struct{}
	release chan struct{}
}

func (p *releasedSubAgentProvider) WithAPIKey(string) Provider { return p }
func (p *releasedSubAgentProvider) Generate(ctx context.Context, req Request) (Response, error) {
	select {
	case <-p.started:
	default:
		close(p.started)
	}
	select {
	case <-p.release:
		return p.subAgentImmediateProvider.Generate(ctx, req)
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
}

func TestSubAgentSuccessfulStaleCompletionCannotMutateReplacement(t *testing.T) {
	p := &releasedSubAgentProvider{started: make(chan struct{}), release: make(chan struct{})}
	agent, parent := newSubAgentTest(t, p)
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	r := &subAgentRunner{manager: manager, parent: parent}
	_ = parent.SetPlan([]string{"original"})
	done := make(chan string, 1)
	go func() {
		report, err := r.Delegate(context.Background(), "original")
		if err != nil {
			done <- "error: " + err.Error()
			return
		}
		done <- report
	}()
	<-p.started
	id := manager.runningJobID()
	revision := parent.Plan().Revision
	_ = parent.SetPlan([]string{"replacement"})
	close(p.release)
	var report string
	select {
	case report = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("blocking delegate never returned")
	}
	if !strings.Contains(report, "status=completed") || !strings.Contains(report, "revision "+uintToStr(revision)) {
		t.Fatalf("lost captured revision: %s", report)
	}
	if state := parent.Plan(); state.Current != 0 || state.Steps[0].Status != "ready" {
		t.Fatalf("stale success changed new plan: %+v", state)
	}
	if err := r.Accept(id, "stale success"); err == nil {
		t.Fatal("accepted stale success")
	}
}

func uintToStr(v uint64) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

func TestSubAgentConcurrentFollowUpReservation(t *testing.T) {
	agent, parent := newSubAgentTest(t, &subAgentImmediateProvider{})
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	r := &subAgentRunner{manager: manager, parent: parent}
	_ = parent.SetPlan([]string{"step"})
	report, err := r.Delegate(context.Background(), "step")
	if err != nil {
		t.Fatal(err)
	}
	id := jobIDFromReport(report)
	p := &subAgentBlockingProvider{started: make(chan struct{})}
	manager.agent.Client.RegisterAdapter(AdapterOpenAI, p)
	var wg sync.WaitGroup
	ids := make(chan string, 16)
	var mu sync.Mutex
	successes := 0
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			retryReport, err := r.FollowUp(ctx, id, "retry")
			if err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
				ids <- jobIDFromReport(retryReport)
			}
		}()
	}
	go func() {
		<-p.started
	}()
	time.Sleep(50 * time.Millisecond)
	if got := manager.runningJobID(); got == "" {
		t.Fatal("no follow-up job running")
	}
	if _, err := r.Delegate(context.Background(), "overlap"); err == nil {
		t.Fatal("delegation overlapped follow-up")
	}
	if err := r.Accept(id, "overlap"); err == nil {
		t.Fatal("accepted while follow-up running")
	}
	if _, err := r.Stop(context.Background(), manager.runningJobID()); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(ids)
	for range ids {
	}
	if successes != 1 {
		t.Fatalf("follow-up reserved %d jobs", successes)
	}
}

func TestSubAgentToolAcceptanceAndFollowUpValidation(t *testing.T) {
	agent, parent := newSubAgentTest(t, &subAgentImmediateProvider{})
	manager := newSubAgentManager(agent, agent.SubAgentConfig)
	r := &subAgentRunner{manager: manager, parent: parent}
	tool := newPlanningToolExecutor(nil, parent)
	tool.ConfigureSubAgent(r)
	if result := tool.Execute(context.Background(), ToolCall{Name: "follow_up_subagent", Arguments: `{"task":"missing id"}`}); !result.IsError {
		t.Fatal("follow-up without ID started investigation")
	}
	if state := parent.Plan(); len(state.Steps) != 0 {
		t.Fatalf("invalid follow-up changed plan: %+v", state)
	}
	_ = parent.SetPlan([]string{"verify"})
	report, err := r.Delegate(context.Background(), "verify")
	if err != nil {
		t.Fatal(err)
	}
	if result := tool.Execute(context.Background(), ToolCall{Name: "accept_subagent_result", Arguments: `{"job_id":"` + jobIDFromReport(report) + `","verification":""}`}); !result.IsError {
		t.Fatal("tool accepted without verification evidence")
	}
	if !strings.Contains(report, "worker done") {
		t.Fatalf("delegation result lost the worker report: %s", report)
	}
	args := `{"job_id":"` + jobIDFromReport(report) + `","verification":"reviewed test evidence"}`
	if result := tool.Execute(context.Background(), ToolCall{Name: "accept_subagent_result", Arguments: args}); result.IsError {
		t.Fatalf("tool acceptance failed: %+v", result)
	}
	if state := parent.Plan(); state.Current != 1 {
		t.Fatalf("tool did not advance: %+v", state)
	}
}
