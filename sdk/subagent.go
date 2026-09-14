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

const defaultSubAgentSystemPrompt = "You are the worker sub-agent. Execute only the task assigned by the planner inside the current project workspace. Do not communicate with the end user. Do not delegate to another agent. Do not change project scope. Inspect, implement, validate, and report the result to the planner."

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

type SubAgentRunner interface {
	Delegate(context.Context, string) (string, error)
	Status(string) string
	History(string) string
	Stop(string) bool
	FollowUp(context.Context, string, string) (string, error)
	Accept(string, string) error
}

type subAgentJob struct {
	id         string
	workerID   string
	revision   uint64
	reviewed   bool
	superseded bool
	accepted   bool
	input      Input
	parent     *Session
	task       string
	status     string
	started    time.Time
	finished   time.Time
	result     string
	progress   string
	events     []string
	plan       string
	step       PlanStep
	planned    bool
	cancel     context.CancelFunc
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

func (e SubAgentEvent) Message() string {
	label := "investigation"
	if e.PlanStep.Index > 0 {
		label = fmt.Sprintf("plan revision %d step %d", e.PlanRevision, e.PlanStep.Index)
	}
	guidance := "Read the terminal result using `subagent_history` or `subagent_status`. Use `follow_up_subagent` with this job ID for failed, blocked, or incomplete work in the same worker session; wait for its completion event rather than polling."
	if e.PlanStep.Index > 0 {
		guidance += " Worker completion is not plan acceptance: call `accept_subagent_result` with verification evidence only after verifying success. Ignore stale results for replacement plans."
	} else {
		guidance += " Investigation results do not require plan-step acceptance; use the reviewed findings to create the execution plan."
	}
	return fmt.Sprintf("<sub agent id %s> %s: %s\n%s", e.JobID, e.Result, label, guidance)
}

type subAgentManager struct {
	mu        sync.RWMutex
	jobs      map[string]*subAgentJob
	agent     *Agent
	cfg       SubAgentConfig
	eventMu   sync.RWMutex
	sink      func(SubAgentEvent)
	traceSink func(SubAgentEvent)
}

func newSubAgentManager(agent *Agent, cfg SubAgentConfig) *subAgentManager {
	return &subAgentManager{jobs: make(map[string]*subAgentJob), agent: agent, cfg: cfg}
}

func (m *subAgentManager) Delegate(parent *Session, task string) (string, error) {
	return m.start(parent, task, "", Input{})
}

func (m *subAgentManager) start(parent *Session, task, previous string, input Input) (string, error) {
	if m == nil || m.agent == nil || parent == nil {
		return "", errors.New("sdk: sub-agent is not configured")
	}
	task = strings.TrimSpace(task)
	if task == "" {
		return "", errors.New("sdk: sub-agent task is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var retry *subAgentJob
	if previous != "" {
		retry = m.jobs[previous]
		if retry == nil || retry.parent != parent {
			return "", errors.New("sdk: sub-agent job not found")
		}
		if retry.status == "running" || retry.accepted || retry.superseded {
			return "", errors.New("sdk: job is not retryable")
		}
	}
	id := fmt.Sprintf("sa-%d", time.Now().UnixNano())
	plan, step, planned, err := parent.reserveSubAgent(id, retry)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &subAgentJob{id: id, workerID: parent.ID() + ":subagent:" + id, parent: parent, task: task, status: "running", started: time.Now(), cancel: cancel, step: step, planned: planned, revision: plan.Revision, input: cloneInputRoute(input)}
	if retry != nil {
		job.workerID = retry.workerID
		job.input = cloneInputRoute(retry.input)
		retry.superseded = true
	}
	if planned {
		job.plan = formatPlan(plan)
	}
	m.jobs[id] = job
	go m.run(ctx, job)
	return id, nil
}

func (m *subAgentManager) run(ctx context.Context, job *subAgentJob) {
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
	m.eventMu.RLock()
	sink := m.sink
	m.eventMu.RUnlock()
	if sink != nil {
		sink(SubAgentEvent{Parent: job.parent, JobID: job.id, Status: status, Result: result, PlanStep: job.step, PlanRevision: job.revision, Input: cloneInputRoute(job.input)})
	}
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
		message := TraceMessage(event)
		m.mu.Lock()
		job.progress = message
		if message != "" {
			job.events = append(job.events, message)
		}
		m.mu.Unlock()
		m.emitTrace(job, TraceEvent{Stage: event.Stage, Message: event.Message, Response: cloneResponsePtr(event.Response), ToolCall: cloneToolCallPtr(event.ToolCall), ToolResult: cloneToolResultPtr(event.ToolResult), Text: event.Text, Err: event.Err, RetryAfter: event.RetryAfter, Elapsed: event.Elapsed})
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

func (m *subAgentManager) Status(parent *Session, id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[id]
	if job == nil || job.parent != parent {
		return "sub-agent job not found: " + id
	}
	if job.status != "running" {
		job.reviewed = true
	}
	stepLabel := "investigation"
	if job.planned {
		stepLabel = fmt.Sprintf("%d", job.step.Index)
	}
	text := fmt.Sprintf("job=%s status=%s step=%s revision=%d accepted=%t superseded=%t task=%s", job.id, job.status, stepLabel, job.revision, job.accepted, job.superseded, job.task)
	if job.progress != "" {
		text += " progress=" + job.progress
	}
	if job.result != "" {
		text += " result=" + job.result
	}
	return text
}

func (m *subAgentManager) History(parent *Session, id string) string {
	m.mu.Lock()
	job := m.jobs[id]
	if job == nil || job.parent != parent {
		m.mu.Unlock()
		return "sub-agent job not found: " + id
	}
	if job.status != "running" {
		job.reviewed = true
	}
	events := append([]string(nil), job.events...)
	result := job.result
	m.mu.Unlock()
	var b strings.Builder
	for _, event := range events {
		if strings.TrimSpace(event) != "" {
			b.WriteString(event)
			b.WriteByte('\n')
		}
	}
	if result != "" {
		b.WriteString("result: ")
		b.WriteString(result)
	}
	return strings.TrimSpace(b.String())
}

func (m *subAgentManager) SetEventSink(sink func(SubAgentEvent)) {
	m.eventMu.Lock()
	m.sink = sink
	m.eventMu.Unlock()
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

func (m *subAgentManager) Stop(parent *Session, id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job := m.jobs[id]
	if job == nil || job.parent != parent || job.cancel == nil {
		return false
	}
	job.cancel()
	return true
}

func (m *subAgentManager) Accept(parent *Session, id, verification string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[id]
	if job == nil || job.parent != parent {
		return errors.New("sdk: sub-agent job not found")
	}
	if job.status != "completed" || !job.reviewed || job.accepted || job.superseded || strings.TrimSpace(verification) == "" {
		return errors.New("sdk: read the terminal result/history and provide verified success before acceptance; failed/incomplete work needs follow-up")
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
	if err := ctx.Err(); err != nil {
		return "", err
	}
	input, _ := ctx.Value(lifecycleInputKey{}).(Input)
	return r.manager.start(r.parent, task, "", input)
}
func (r *subAgentRunner) Status(id string) string  { return r.manager.Status(r.parent, id) }
func (r *subAgentRunner) History(id string) string { return r.manager.History(r.parent, id) }
func (r *subAgentRunner) Stop(id string) bool      { return r.manager.Stop(r.parent, id) }

func (r *subAgentRunner) FollowUp(ctx context.Context, id, task string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("sdk: follow-up job_id is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return r.manager.start(r.parent, task, id, Input{})
}
func (r *subAgentRunner) Accept(id, verification string) error {
	return r.manager.Accept(r.parent, id, verification)
}

type subAgentTool struct{ runner SubAgentRunner }

func (t *subAgentTool) Definitions() []Tool {
	return []Tool{
		{Name: "follow_up_subagent", Description: "Retry or clarify a terminal, unaccepted job in the same worker session. Returns a new job ID; wait for its completion event.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}, "task": map[string]any{"type": "string"}}, "required": []string{"job_id", "task"}}},
		{Name: "accept_subagent_result", Description: "Explicitly accept verified success of the current plan step, advancing exactly one step. First read the terminal result/history and provide verified success evidence.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}, "verification": map[string]any{"type": "string"}}, "required": []string{"job_id", "verification"}}},
		{Name: "delegate_to_subagent", Description: "Start a background worker task. Returns immediately with a job id; the worker runs in a separate session and reports completion, failure, or stop to the planner.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"task": map[string]any{"type": "string"}}, "required": []string{"task"}}},
		{Name: "subagent_status", Description: "Read progress/result on explicit request or when needed for review. Wait for completion events rather than repeatedly polling.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}}, "required": []string{"job_id"}}},
		{Name: "subagent_history", Description: "Read the execution history and final result of a sub-agent job without reading repository source directly.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}}, "required": []string{"job_id"}}},
		{Name: "stop_subagent", Description: "Stop a running background sub-agent job when the user changes direction or the task should be cancelled.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}}, "required": []string{"job_id"}}},
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
		id, err := t.runner.FollowUp(ctx, strings.TrimSpace(input.JobID), input.Task)
		if err != nil {
			return ToolResult{ID: call.ID, Content: err.Error(), IsError: true}
		}
		result.Content = "sub-agent follow-up started: " + id
		return result
	case "accept_subagent_result":
		if err := t.runner.Accept(strings.TrimSpace(input.JobID), input.Verification); err != nil {
			return ToolResult{ID: call.ID, Content: err.Error(), IsError: true}
		}
		result.Content = "Verified result accepted; the next plan step is now ready, if any."
		return result
	case "delegate_to_subagent":
		id, err := t.runner.Delegate(ctx, input.Task)
		if err != nil {
			result.Content = err.Error()
			result.IsError = true
			return result
		}
		result.Content = "sub-agent started: " + id
		return result
	case "subagent_status":
		result.Content = t.runner.Status(strings.TrimSpace(input.JobID))
		return result
	case "subagent_history":
		result.Content = t.runner.History(strings.TrimSpace(input.JobID))
		return result
	case "stop_subagent":
		if t.runner.Stop(strings.TrimSpace(input.JobID)) {
			result.Content = "sub-agent stop requested: " + input.JobID
		} else {
			result.Content = "sub-agent is not running or job was not found: " + input.JobID
			result.IsError = true
		}
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
