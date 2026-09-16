package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const defaultSubAgentSystemPrompt = "You are the worker sub-agent. Execute only the task assigned by the planner inside the current project workspace. Work, think out loud in tool usage, and write your handoff report entirely in English; the task you receive is already in English. Do not communicate with the end user. Do not delegate to another agent. Do not change project scope. Inspect, implement, validate, and report the result to the planner. Validate with the minimal sufficient check: one command that proves the outcome (or a single combined shell line for related checks). Do not repeat equivalent listings of the same target once the outcome is proven, and do not try another command formulation after a check already succeeded. Your final message is the planner's only report: state exactly what changed (file paths), how you validated it (the command and its outcome), and anything left unresolved - the planner cannot inspect the workspace itself."

type SubAgentConfig struct {
	Enabled         bool
	Provider        string
	Model           string
	MaxOutputTokens int
	Temperature     *float64
	ThinkingLevel   ThinkingLevel
	SystemPrompt    string
	Workspace       string
}

// SubAgentRunner is the planner-facing orchestration surface. Every operation
// is blocking: it waits until the worker job reaches a terminal state and
// returns the full handoff report as its result (REQ-019, REQ-034).
type SubAgentRunner interface {
	Delegate(context.Context, string) (string, error)
	Status(string) string
	Stop(context.Context, string) (string, error)
	FollowUp(context.Context, string, string) (string, error)
	Continue(context.Context, string, string) (string, error)
	Accept(string, string) error
}

const subAgentReportToolResultRunes = 1000

type toolHistoryEntry struct {
	ID        string
	Name      string
	Arguments string
	Result    string
	IsError   bool
	completed bool
}

type subAgentJob struct {
	id         string
	workerID   string
	revision   uint64
	reviewed   bool
	superseded bool
	accepted   bool
	continued  bool
	input      Input
	parent     *Session
	task       string
	status     string
	started    time.Time
	finished   time.Time
	result     string
	progress   string
	events     []string
	tools      []toolHistoryEntry
	plan       string
	step       PlanStep
	planned    bool
	cancel     context.CancelFunc
	done       chan struct{}
}

type SubAgentEvent struct {
	Parent       *Session
	JobID        string
	Status       string
	Result       string
	PlanStep     PlanStep
	PlanRevision uint64
	Input        Input
	Trace        *TraceEvent
}

type subAgentManager struct {
	mu        sync.RWMutex
	jobs      map[string]*subAgentJob
	agent     *Agent
	cfg       SubAgentConfig
	eventMu   sync.RWMutex
	traceSink func(SubAgentEvent)
}

func newSubAgentManager(agent *Agent, cfg SubAgentConfig) *subAgentManager {
	return &subAgentManager{jobs: make(map[string]*subAgentJob), agent: agent, cfg: cfg}
}

func (m *subAgentManager) start(parent *Session, task, previous string, input Input) (*subAgentJob, error) {
	if m == nil || m.agent == nil || parent == nil {
		return nil, errors.New("sdk: sub-agent is not configured")
	}
	task = strings.TrimSpace(task)
	if task == "" {
		return nil, errors.New("sdk: sub-agent task is required")
	}
	m.mu.Lock()
	var retry *subAgentJob
	if previous != "" {
		retry = m.jobs[previous]
		if retry == nil || retry.parent != parent {
			m.mu.Unlock()
			return nil, errors.New("sdk: sub-agent job not found")
		}
		if retry.status == "running" || retry.accepted || retry.superseded {
			m.mu.Unlock()
			return nil, errors.New("sdk: job is not retryable")
		}
	}
	id := fmt.Sprintf("sa-%d", time.Now().UnixNano())
	plan, step, planned, err := parent.reserveSubAgent(id, retry)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &subAgentJob{id: id, workerID: parent.ID() + ":subagent:" + id, parent: parent, task: task, status: "running", started: time.Now(), cancel: cancel, step: step, planned: planned, revision: plan.Revision, input: cloneInputRoute(input), done: make(chan struct{})}
	if retry != nil {
		job.workerID = retry.workerID
		job.input = cloneInputRoute(retry.input)
		retry.superseded = true
	}
	if planned {
		job.plan = formatPlan(plan)
	}
	m.jobs[id] = job
	m.mu.Unlock()
	go m.run(ctx, job)
	return job, nil
}

func (m *subAgentManager) startContinue(parent *Session, task, previous string, input Input) (*subAgentJob, error) {
	if m == nil || m.agent == nil || parent == nil {
		return nil, errors.New("sdk: sub-agent is not configured")
	}
	task = strings.TrimSpace(task)
	if task == "" {
		return nil, errors.New("sdk: sub-agent task is required")
	}
	previous = strings.TrimSpace(previous)
	if previous == "" {
		return nil, errors.New("sdk: continue job_id is required")
	}
	m.mu.Lock()
	prev := m.jobs[previous]
	if prev == nil || prev.parent != parent {
		m.mu.Unlock()
		return nil, errors.New("sdk: sub-agent job not found")
	}
	if prev.status == "running" {
		m.mu.Unlock()
		return nil, errors.New("sdk: previous job is still running")
	}
	id := fmt.Sprintf("sa-%d", time.Now().UnixNano())
	plan, step, planned, err := parent.reserveSubAgentForContinue(id)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &subAgentJob{id: id, workerID: prev.workerID, parent: parent, task: task, status: "running", started: time.Now(), cancel: cancel, step: step, planned: planned, revision: plan.Revision, input: cloneInputRoute(prev.input), done: make(chan struct{})}
	if planned {
		job.plan = formatPlan(plan)
	}
	prev.continued = true
	m.jobs[id] = job
	m.mu.Unlock()
	go m.run(ctx, job)
	return job, nil
}

// await blocks until the job reaches its terminal state, cancelling the
// worker when the caller's context ends first, and then returns the full
// handoff report (REQ-019). The report marks the job reviewed so acceptance
// remains an explicit separate action (REQ-020).
func (m *subAgentManager) await(ctx context.Context, job *subAgentJob) string {
	select {
	case <-job.done:
	case <-ctx.Done():
		m.cancelJob(job)
		<-job.done
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	job.reviewed = true
	return m.reportLocked(job)
}

func (m *subAgentManager) cancelJob(job *subAgentJob) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if job.cancel != nil {
		job.cancel()
	}
}

func (m *subAgentManager) recordToolEvent(job *subAgentJob, event TraceEvent) string {
	switch event.Stage {
	case TraceToolCall, TraceToolRunning:
		if event.ToolCall == nil {
			return ""
		}
		call := *event.ToolCall
		for i := range job.tools {
			if job.tools[i].ID == call.ID {
				return ""
			}
		}
		job.tools = append(job.tools, toolHistoryEntry{ID: call.ID, Name: call.Name, Arguments: call.Arguments})
		if strings.TrimSpace(call.Arguments) != "" {
			return fmt.Sprintf("tool %s started (id=%s) args=%s", call.Name, call.ID, call.Arguments)
		}
		return fmt.Sprintf("tool %s started (id=%s)", call.Name, call.ID)
	case TraceToolResult:
		if event.ToolResult == nil {
			return ""
		}
		res := *event.ToolResult
		name := ""
		for i := range job.tools {
			if job.tools[i].ID == res.ID {
				job.tools[i].Result = res.Content
				job.tools[i].IsError = res.IsError
				job.tools[i].completed = true
				name = job.tools[i].Name
				break
			}
		}
		if name == "" && event.ToolCall != nil {
			name = event.ToolCall.Name
			job.tools = append(job.tools, toolHistoryEntry{ID: res.ID, Name: name, Result: res.Content, IsError: res.IsError, completed: true})
		}
		label := "completed"
		if res.IsError {
			label = "failed"
		}
		if name != "" {
			if res.Content != "" {
				return fmt.Sprintf("tool %s %s (id=%s) result=%s", name, label, res.ID, res.Content)
			}
			return fmt.Sprintf("tool %s %s (id=%s)", name, label, res.ID)
		}
		if res.Content != "" {
			return fmt.Sprintf("tool result %s (id=%s) result=%s", label, res.ID, res.Content)
		}
		return fmt.Sprintf("tool result %s (id=%s)", label, res.ID)
	default:
		return TraceMessage(event)
	}
}

func (m *subAgentManager) run(ctx context.Context, job *subAgentJob) {
	defer close(job.done)
	resp, err := m.runWorker(ctx, job)
	status, result := "completed", responseText(resp)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			status, result = "stopped", "sub-agent was stopped before completion"
		} else {
			status, result = "failed", err.Error()
		}
	}
	m.mu.Lock()
	job.status, job.result, job.finished, job.cancel = status, result, time.Now(), nil
	job.parent.finishSubAgent(job, status)
	m.mu.Unlock()
}

func (m *subAgentManager) emitTrace(job *subAgentJob, event TraceEvent) {
	if m == nil || job == nil {
		return
	}
	m.eventMu.RLock()
	sink := m.traceSink
	m.eventMu.RUnlock()
	if sink == nil {
		return
	}
	trace := event
	sink(SubAgentEvent{Parent: job.parent, JobID: job.id, Status: job.status, PlanStep: job.step, PlanRevision: job.revision, Input: cloneInputRoute(job.input), Trace: &trace})
}

func (m *subAgentManager) runWorker(ctx context.Context, job *subAgentJob) (Response, error) {
	parentCfg := job.parent.Config()
	provider := strings.TrimSpace(m.cfg.Provider)
	if provider == "" {
		provider = string(parentCfg.Provider)
	}
	model := strings.TrimSpace(m.cfg.Model)
	if model == "" {
		model = parentCfg.Model
	}
	keys := job.parent.keys
	if provider != string(parentCfg.Provider) {
		config, err := m.agent.Client.Router.Provider(ProviderID(provider))
		if err != nil {
			return Response{}, err
		}
		keys = config.Keys
		if keys == nil {
			return Response{}, fmt.Errorf("sdk: sub-agent provider %q has no key pool", provider)
		}
	}
	workerID := job.workerID
	store := job.parent.store
	if store == nil {
		return Response{}, errors.New("sdk: parent session is not persistent")
	}
	worker, err := OpenSession(SessionDBPath(store.Dir(), workerID), SessionConfig{ID: workerID, Provider: ProviderID(provider), Model: model, KeyIndex: parentCfg.KeyIndex, ThinkingLevel: chooseThinking(m.cfg.ThinkingLevel, parentCfg.ThinkingLevel), Temperature: parentCfg.Temperature}, keys)
	if err != nil {
		return Response{}, err
	}
	defer worker.Close()
	if m.cfg.Temperature != nil {
		if err := worker.SetTemperature(*m.cfg.Temperature); err != nil {
			return Response{}, err
		}
	}
	prompt := strings.TrimSpace(m.cfg.SystemPrompt)
	if prompt == "" {
		prompt = defaultSubAgentSystemPrompt
	}
	if job.planned {
		prompt += "\n\nCurrent plan:\n" + job.plan
		prompt += "\n\nCurrent plan step:\n" + job.step.Text
	} else {
		prompt += "\n\nInvestigation mode: inspect the repository and return only the requested findings. Do not modify the project unless the investigation task explicitly requires it."
	}
	if requirements := projectRequirements(m.cfg.Workspace); requirements != "" {
		prompt += "\n\nProject requirements from the repository:\n" + requirements
	}
	workerAgent := &Agent{Client: m.agent.Client, Tools: m.agent.Tools, MaxRetries: m.agent.MaxRetries, DisablePlanning: true, SubAgentConfig: SubAgentConfig{Enabled: false}}
	req := Request{Provider: ProviderID(provider), Model: model, SystemPrompt: prompt, MaxOutputTokens: m.cfg.MaxOutputTokens, ThinkingLevel: worker.Config().ThinkingLevel, Temperature: worker.Config().Temperature}
	trace := func(_ context.Context, event TraceEvent) {
		m.mu.Lock()
		message := m.recordToolEvent(job, event)
		job.progress = message
		if message != "" {
			job.events = append(job.events, message)
		}
		m.mu.Unlock()
		m.emitTrace(job, TraceEvent{Stage: event.Stage, Message: event.Message, Response: cloneResponsePtr(event.Response), ToolCall: cloneToolCallPtr(event.ToolCall), ToolResult: cloneToolResultPtr(event.ToolResult), Text: event.Text, Err: event.Err, RetryAfter: event.RetryAfter, Elapsed: event.Elapsed, RequestStartedMs: event.RequestStartedMs, ProviderAcceptedMs: event.ProviderAcceptedMs, AtMs: event.AtMs})
	}
	return workerAgent.runTurn(ctx, worker, Turn{Role: RoleUser, Content: []ContentPart{{Type: ContentText, Text: job.task}}}, req, trace, nil)
}

func cloneResponsePtr(in *Response) *Response {
	if in == nil {
		return nil
	}
	out := *in
	out.Content = append([]ContentPart(nil), in.Content...)
	out.ToolCalls = append([]ToolCall(nil), in.ToolCalls...)
	return &out
}

func cloneToolCallPtr(in *ToolCall) *ToolCall {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneToolResultPtr(in *ToolResult) *ToolResult {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func chooseThinking(value, fallback ThinkingLevel) ThinkingLevel {
	if value != "" {
		return value
	}
	return fallback
}

// reportLocked renders the complete handoff report for one job: status,
// summary, and every worker tool with arguments and (length-capped) result,
// so the planner can verify the work without any further tool calls.
// The caller must hold m.mu (REQ-019, REQ-034).
func (m *subAgentManager) reportLocked(job *subAgentJob) string {
	stepLabel := "investigation"
	if job.planned {
		stepLabel = fmt.Sprintf("plan revision %d step %d", job.revision, job.step.Index)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "sub-agent %s: status=%s job=%s %s worker=%s\n", job.id, job.status, job.id, stepLabel, job.workerID)
	fmt.Fprintf(&b, "task: %s\n", job.task)
	if len(job.tools) == 0 {
		b.WriteString("tools_used: none\n")
	} else {
		fmt.Fprintf(&b, "tools_used: %d\n", len(job.tools))
		for i, tool := range job.tools {
			fmt.Fprintf(&b, "tool %d: name=%s id=%s\n", i+1, tool.Name, tool.ID)
			if strings.TrimSpace(tool.Arguments) != "" {
				b.WriteString("  args: " + truncateRunes(oneLineText(tool.Arguments), subAgentReportToolResultRunes) + "\n")
			} else {
				b.WriteString("  args: (none)\n")
			}
			if !tool.completed {
				b.WriteString("  result: (pending)\n")
			} else if tool.IsError {
				b.WriteString("  error: " + truncateRunes(tool.Result, subAgentReportToolResultRunes) + "\n")
			} else {
				b.WriteString("  result: " + truncateRunes(tool.Result, subAgentReportToolResultRunes) + "\n")
			}
		}
	}
	if job.result != "" {
		b.WriteString("result: ")
		b.WriteString(job.result)
		b.WriteString("\n")
	}
	b.WriteString("This report is delivered in full inside the blocking tool call that just returned. Verify it, then either accept the step with `accept_subagent_result` (verified success only), retry blocked/failed work with `follow_up_subagent`, or order new work into the same worker session with `continue_subagent`.")
	return strings.TrimSpace(b.String())
}

func truncateRunes(text string, max int) string {
	if max <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max-1]) + "…"
}

func oneLineText(text string) string { return strings.Join(strings.Fields(text), " ") }

// statusText renders one-line job state; kept for non-LLM diagnostics only
// (the model-facing status/history tools were removed with CHANGE-022).
func (m *subAgentManager) statusText(parent *Session, id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[id]
	if job == nil || job.parent != parent {
		return "sub-agent job not found: " + id
	}
	stepLabel := "investigation"
	if job.planned {
		stepLabel = fmt.Sprintf("%d", job.step.Index)
	}
	text := fmt.Sprintf("job=%s status=%s step=%s revision=%d accepted=%t superseded=%t continued=%t worker=%s tools=%d task=%s", job.id, job.status, stepLabel, job.revision, job.accepted, job.superseded, job.continued, job.workerID, len(job.tools), job.task)
	if len(job.tools) > 0 {
		names := make([]string, 0, len(job.tools))
		for _, tool := range job.tools {
			status := "ok"
			if !tool.completed {
				status = "running"
			} else if tool.IsError {
				status = "error"
			}
			names = append(names, tool.Name+"("+status+")")
		}
		text += " tools_used=" + strings.Join(names, ",")
	}
	if job.progress != "" {
		text += " progress=" + job.progress
	}
	return text
}

func (m *subAgentManager) SetTraceSink(sink func(SubAgentEvent)) {
	m.eventMu.Lock()
	m.traceSink = sink
	m.eventMu.Unlock()
}

func formatPlan(plan PlanState) string {
	var b strings.Builder
	for _, step := range plan.Steps {
		b.WriteString(fmt.Sprintf("%d. %s\n", step.Index, step.Text))
	}
	return strings.TrimSpace(b.String())
}

// stop blocks until the job has actually stopped and then returns the final
// report, instead of acknowledging the request and reporting later
// (REQ-020, REQ-034).
func (m *subAgentManager) stop(ctx context.Context, parent *Session, id string) (string, error) {
	m.mu.RLock()
	job := m.jobs[id]
	running := job != nil && job.parent == parent && job.status == "running"
	m.mu.RUnlock()
	if job == nil || job.parent != parent {
		return "", errors.New("sdk: sub-agent job not found: " + id)
	}
	if !running {
		return "", errors.New("sdk: sub-agent is not running: " + id)
	}
	m.cancelJob(job)
	return m.await(ctx, job), nil
}

func (m *subAgentManager) Accept(parent *Session, id, verification string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[id]
	if job == nil || job.parent != parent {
		return errors.New("sdk: sub-agent job not found")
	}
	if job.status != "completed" || !job.reviewed || job.accepted || job.superseded || strings.TrimSpace(verification) == "" {
		return errors.New("sdk: read the terminal report delivered by the delegation call and provide verified success before acceptance; failed/incomplete work needs follow-up")
	}
	if err := parent.acceptSubAgent(job); err != nil {
		return err
	}
	job.accepted = true
	job.events = append(job.events, "Main accepted verified success: "+verification)
	return nil
}

type subAgentRunner struct {
	manager *subAgentManager
	parent  *Session
}

func (r *subAgentRunner) Delegate(ctx context.Context, task string) (string, error) {
	input, _ := ctx.Value(lifecycleInputKey{}).(Input)
	job, err := r.manager.start(r.parent, task, "", input)
	if err != nil {
		return "", err
	}
	return r.manager.await(ctx, job), nil
}
func (r *subAgentRunner) Status(id string) string { return r.manager.statusText(r.parent, id) }

func (r *subAgentRunner) Stop(ctx context.Context, id string) (string, error) {
	return r.manager.stop(ctx, r.parent, strings.TrimSpace(id))
}

func (r *subAgentRunner) FollowUp(ctx context.Context, id, task string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("sdk: follow-up job_id is required")
	}
	job, err := r.manager.start(r.parent, task, id, Input{})
	if err != nil {
		return "", err
	}
	return r.manager.await(ctx, job), nil
}
func (r *subAgentRunner) Continue(ctx context.Context, id, task string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("sdk: continue job_id is required")
	}
	input, _ := ctx.Value(lifecycleInputKey{}).(Input)
	job, err := r.manager.startContinue(r.parent, task, id, input)
	if err != nil {
		return "", err
	}
	return r.manager.await(ctx, job), nil
}
func (r *subAgentRunner) Accept(id, verification string) error {
	return r.manager.Accept(r.parent, id, verification)
}

type subAgentTool struct{ runner SubAgentRunner }

func (t *subAgentTool) Definitions() []Tool {
	return []Tool{
		{Name: "follow_up_subagent", Description: "Retry or clarify a terminal, unaccepted job in the same worker session. Blocking: returns the complete handoff report of the retry when it finishes.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}, "task": map[string]any{"type": "string"}}, "required": []string{"job_id", "task"}}},
		{Name: "continue_subagent", Description: "Order new work into the same worker session of a terminal job, keeping its history. Blocking: returns the complete handoff report when the new job finishes.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}, "task": map[string]any{"type": "string"}}, "required": []string{"job_id", "task"}}},
		{Name: "accept_subagent_result", Description: "Explicitly accept verified success of the current plan step, advancing exactly one step. Verify against the report already delivered by the blocking delegation call.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}, "verification": map[string]any{"type": "string"}}, "required": []string{"job_id", "verification"}}},
		{Name: "delegate_to_subagent", Description: "Assign one task to the worker and wait for it to finish. Returns the complete handoff report (status, every worker tool with arguments and result, validation evidence, final summary) as this call's result - no separate status or history lookup is needed.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"task": map[string]any{"type": "string"}}, "required": []string{"task"}}},
		{Name: "stop_subagent", Description: "Stop a running worker job and wait for it to actually stop. Returns the final report (stopped status plus progress and tool history) as this call's result.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}}, "required": []string{"job_id"}}},
	}
}
func (t *subAgentTool) Execute(ctx context.Context, call ToolCall) ToolResult {
	result := ToolResult{ID: call.ID}
	var input struct {
		Task         string `json:"task"`
		JobID        string `json:"job_id"`
		Verification string `json:"verification"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
		result.Content = "invalid sub-agent arguments: " + err.Error()
		result.IsError = true
		return result
	}
	if t == nil || t.runner == nil {
		result.Content = "sub-agent is not configured"
		result.IsError = true
		return result
	}
	switch call.Name {
	case "follow_up_subagent":
		report, err := t.runner.FollowUp(ctx, strings.TrimSpace(input.JobID), input.Task)
		if err != nil {
			return ToolResult{ID: call.ID, Content: err.Error(), IsError: true}
		}
		result.Content = report
		return result
	case "continue_subagent":
		report, err := t.runner.Continue(ctx, strings.TrimSpace(input.JobID), input.Task)
		if err != nil {
			return ToolResult{ID: call.ID, Content: err.Error(), IsError: true}
		}
		result.Content = report
		return result
	case "accept_subagent_result":
		if err := t.runner.Accept(strings.TrimSpace(input.JobID), input.Verification); err != nil {
			return ToolResult{ID: call.ID, Content: err.Error(), IsError: true}
		}
		result.Content = "Verified result accepted; the next plan step is now ready, if any."
		return result
	case "delegate_to_subagent":
		report, err := t.runner.Delegate(ctx, input.Task)
		if err != nil {
			result.Content = err.Error()
			result.IsError = true
			return result
		}
		result.Content = report
		return result
	case "stop_subagent":
		report, err := t.runner.Stop(ctx, strings.TrimSpace(input.JobID))
		if err != nil {
			result.Content = err.Error()
			result.IsError = true
			return result
		}
		result.Content = report
		return result
	default:
		result.Content = "unknown sub-agent operation"
		result.IsError = true
		return result
	}
}

func responseText(resp Response) string {
	var b strings.Builder
	for _, part := range resp.Content {
		if strings.TrimSpace(part.Text) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(part.Text)
	}
	if b.Len() == 0 {
		return "sub-agent completed without a text result"
	}
	return b.String()
}

// runningJobID reports the parent's currently running job, for diagnostics.
func (m *subAgentManager) runningJobID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, job := range m.jobs {
		if job.status == "running" {
			return job.id
		}
	}
	return ""
}

// startAndRegister starts a job without waiting; tests use it to exercise
// stop/overlap paths that the blocking API no longer exposes directly.
func (m *subAgentManager) startAndRegister(parent *Session, task string) (string, error) {
	job, err := m.start(parent, task, "", Input{})
	if err != nil {
		return "", err
	}
	return job.id, nil
}
