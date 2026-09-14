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
	Send(id, message string) (string, error)
	Stop(string) bool
}

// orchestrationToolNames are Main Agent tools. Workers must never see or
// execute them: a worker that can plan or delegate stops being a worker.
var orchestrationToolNames = map[string]bool{
	planningToolName:       true,
	"delegate_to_subagent": true,
	"subagent_status":      true,
	"subagent_history":     true,
	"send_to_subagent":     true,
	"stop_subagent":        true,
}

// workerToolExecutor exposes only execution tools to a worker sub-agent.
// Even if the underlying registry ever contained an orchestration tool,
// the worker can neither see it nor execute it.
type workerToolExecutor struct{ base ToolExecutor }

func (w *workerToolExecutor) Definitions() []Tool {
	if w == nil || w.base == nil {
		return nil
	}
	var out []Tool
	for _, def := range w.base.Definitions() {
		if orchestrationToolNames[def.Name] {
			continue
		}
		out = append(out, def)
	}
	return out
}

func (w *workerToolExecutor) Execute(ctx context.Context, call ToolCall) ToolResult {
	if orchestrationToolNames[call.Name] {
		return ToolResult{ID: call.ID, Content: "worker sub-agents cannot delegate or plan; execute the assigned task directly", IsError: true}
	}
	if w == nil || w.base == nil {
		return ToolResult{ID: call.ID, Content: "tool execution is not configured", IsError: true}
	}
	return w.base.Execute(ctx, call)
}

type subAgentJob struct {
	id       string
	parent   *Session
	workerID string
	task     string
	status   string
	started  time.Time
	finished time.Time
	result   string
	progress string
	events   []string
	plan     string
	step     PlanStep
	planned  bool
	cancel   context.CancelFunc
}

type SubAgentEvent struct {
	Parent   *Session
	JobID    string
	Status   string
	Result   string
	PlanStep PlanStep
}

type subAgentManager struct {
	mu      sync.RWMutex
	jobs    map[string]*subAgentJob
	agent   *Agent
	cfg     SubAgentConfig
	eventMu sync.RWMutex
	sink    func(SubAgentEvent)
}

func newSubAgentManager(agent *Agent, cfg SubAgentConfig) *subAgentManager {
	return &subAgentManager{jobs: make(map[string]*subAgentJob), agent: agent, cfg: cfg}
}

func (m *subAgentManager) Delegate(parent *Session, task string) (string, error) {
	if m == nil || m.agent == nil || parent == nil {
		return "", errors.New("sdk: sub-agent is not configured")
	}
	task = strings.TrimSpace(task)
	planState := parent.Plan()
	step, planned := PlanStep{}, len(planState.Steps) > 0
	if planned {
		var ok bool
		step, ok = parent.StartCurrentPlanStep()
		if !ok {
			return "", errors.New("sdk: no current plan step is ready for delegation")
		}
	}
	if task == "" {
		if !planned {
			return "", errors.New("sdk: sub-agent task is required for investigation")
		}
		task = step.Text
	}
	m.mu.RLock()
	for _, existing := range m.jobs {
		if existing.parent == parent && existing.status == "running" && existing.planned == planned && (!planned || existing.step.Index == step.Index) {
			m.mu.RUnlock()
			return "", fmt.Errorf("sdk: plan step %s already has a running sub-agent", func() string {
				if planned {
					return fmt.Sprintf("%d", step.Index)
				}
				return "investigation"
			}())
		}
	}
	m.mu.RUnlock()
	id := fmt.Sprintf("sa-%d", time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	job := &subAgentJob{id: id, parent: parent, workerID: parent.ID() + ":subagent:" + id, task: task, status: "running", started: time.Now(), cancel: cancel, step: step, planned: planned}
	if planned {
		job.plan = formatPlan(planState)
	}
	m.mu.Lock()
	m.jobs[id] = job
	m.mu.Unlock()
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
	m.mu.Unlock()
	if job.planned {
		if status == "completed" {
			job.parent.CompleteCurrentPlanStep()
		} else {
			job.parent.FailCurrentPlanStep()
		}
	}
	m.eventMu.RLock()
	sink := m.sink
	m.eventMu.RUnlock()
	if sink != nil {
		sink(SubAgentEvent{Parent: job.parent, JobID: job.id, Status: status, Result: result, PlanStep: job.step})
	}
}

func (m *subAgentManager) runWorker(ctx context.Context, job *subAgentJob) (Response, error) {
	worker, workerAgent, req, err := m.openWorker(job)
	if err != nil {
		return Response{}, err
	}
	defer worker.Close()
	trace := m.jobTrace(job)
	return workerAgent.runTurn(ctx, worker, Turn{Role: RoleUser, Content: []ContentPart{{Type: ContentText, Text: job.task}}}, req, trace, nil)
}

// Send appends a follow-up message to a finished sub-agent session and
// resumes the worker in the background. It returns immediately; completion
// is reported again through the event sink so the Main Agent can read the
// new final summary.
func (m *subAgentManager) Send(id, message string) (string, error) {
	if m == nil {
		return "", errors.New("sdk: sub-agent is not configured")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "", errors.New("sdk: follow-up message is required")
	}
	m.mu.RLock()
	job, ok := m.jobs[strings.TrimSpace(id)]
	m.mu.RUnlock()
	if !ok || job == nil {
		return "", errors.New("sub-agent job not found: " + id)
	}
	m.mu.RLock()
	running := job.status == "running"
	m.mu.RUnlock()
	if running {
		return "", errors.New("sdk: sub-agent job is still running: " + job.id)
	}
	if _, _, _, err := m.openWorker(job); err != nil {
		return "", err
	}
	m.mu.Lock()
	job.status = "running"
	job.task = message
	job.finished = time.Time{}
	job.cancel = nil
	m.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	job.cancel = cancel
	m.mu.Unlock()
	go m.runFollowUp(ctx, job, message)
	return job.id, nil
}

func (m *subAgentManager) runFollowUp(ctx context.Context, job *subAgentJob, message string) {
	worker, workerAgent, req, err := m.openWorker(job)
	var resp Response
	if err == nil {
		defer worker.Close()
		resp, err = workerAgent.runTurn(ctx, worker, Turn{Role: RoleUser, Content: []ContentPart{{Type: ContentText, Text: message}}}, req, m.jobTrace(job), nil)
	}
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
	m.mu.Unlock()
	// Follow-ups never touch plan state: the initial run already advanced
	// the plan step. The Main Agent revises the plan with the plan tool
	// when a follow-up changes the outcome.
	m.emit(SubAgentEvent{Parent: job.parent, JobID: job.id, Status: status, Result: result, PlanStep: job.step})
}

func (m *subAgentManager) emit(event SubAgentEvent) {
	m.eventMu.RLock()
	sink := m.sink
	m.eventMu.RUnlock()
	if sink != nil {
		sink(event)
	}
}

// openWorker reopens the isolated worker session for a job so the initial
// run and every follow-up continue from the same session state.
func (m *subAgentManager) openWorker(job *subAgentJob) (*Session, *Agent, Request, error) {
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
			return nil, nil, Request{}, err
		}
		keys = config.Keys
		if keys == nil {
			return nil, nil, Request{}, fmt.Errorf("sdk: sub-agent provider %q has no key pool", provider)
		}
	}
	store := job.parent.store
	if store == nil {
		return nil, nil, Request{}, errors.New("sdk: parent session is not persistent")
	}
	worker, err := OpenSession(SessionDBPath(store.Dir(), job.workerID), SessionConfig{ID: job.workerID, Provider: ProviderID(provider), Model: model, KeyIndex: parentCfg.KeyIndex, ThinkingLevel: chooseThinking(m.cfg.ThinkingLevel, parentCfg.ThinkingLevel), Temperature: parentCfg.Temperature}, keys)
	if err != nil {
		return nil, nil, Request{}, err
	}
	if m.cfg.Temperature != nil {
		if err := worker.SetTemperature(*m.cfg.Temperature); err != nil {
			_ = worker.Close()
			return nil, nil, Request{}, err
		}
	}
	workerAgent := &Agent{
		Client:          m.agent.Client,
		Tools:           &workerToolExecutor{base: m.agent.Tools},
		MaxRetries:      m.agent.MaxRetries,
		DisablePlanning: true,
		SubAgentConfig:  SubAgentConfig{Enabled: false},
	}
	req := Request{Provider: ProviderID(provider), Model: model, SystemPrompt: m.workerPrompt(job), MaxOutputTokens: m.cfg.MaxOutputTokens, ThinkingLevel: worker.Config().ThinkingLevel, Temperature: worker.Config().Temperature}
	return worker, workerAgent, req, nil
}

func (m *subAgentManager) workerPrompt(job *subAgentJob) string {
	prompt := strings.TrimSpace(m.cfg.SystemPrompt)
	if prompt == "" {
		prompt = "You are the worker sub-agent. Execute only the task assigned by the planner inside the current project workspace. Do not communicate with the end user. Do not delegate to another agent. Inspect, implement, validate, and report the result to the planner."
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
	return prompt
}

func (m *subAgentManager) jobTrace(job *subAgentJob) TraceFunc {
	return func(_ context.Context, event TraceEvent) {
		message := TraceMessage(event)
		m.mu.Lock()
		job.progress = message
		if message != "" {
			job.events = append(job.events, message)
		}
		m.mu.Unlock()
	}
}

func chooseThinking(value, fallback ThinkingLevel) ThinkingLevel {
	if value != "" {
		return value
	}
	return fallback
}

func (m *subAgentManager) Status(id string) string {
	m.mu.RLock()
	job := m.jobs[id]
	m.mu.RUnlock()
	if job == nil {
		return "sub-agent job not found: " + id
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	stepLabel := "investigation"
	if job.planned {
		stepLabel = fmt.Sprintf("%d", job.step.Index)
	}
	text := fmt.Sprintf("job=%s status=%s step=%s task=%s", job.id, job.status, stepLabel, job.task)
	if job.progress != "" {
		text += " progress=" + job.progress
	}
	if job.result != "" {
		text += " result=" + job.result
	}
	return text
}

func (m *subAgentManager) History(id string) string {
	m.mu.RLock()
	job := m.jobs[id]
	if job == nil {
		m.mu.RUnlock()
		return "sub-agent job not found: " + id
	}
	events := append([]string(nil), job.events...)
	result := job.result
	m.mu.RUnlock()
	var b strings.Builder
	for _, event := range events {
		if strings.TrimSpace(event) != "" {
			b.WriteString(event)
			b.WriteByte('\n')
		}
	}
	if summary := m.workerFinalSummary(job); summary != "" {
		b.WriteString("final summary: ")
		b.WriteString(summary)
		b.WriteByte('\n')
	}
	if result != "" {
		b.WriteString("result: ")
		b.WriteString(result)
	}
	return strings.TrimSpace(b.String())
}

// workerFinalSummary reads the last model message from the isolated worker
// session so the Main Agent sees what the sub-agent actually concluded.
func (m *subAgentManager) workerFinalSummary(job *subAgentJob) string {
	if m == nil || job == nil || job.parent == nil || job.parent.store == nil || job.workerID == "" {
		return ""
	}
	worker, _, _, err := m.openWorker(job)
	if err != nil {
		return ""
	}
	defer worker.Close()
	history := worker.History()
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != RoleModel {
			continue
		}
		var b strings.Builder
		for _, part := range history[i].Content {
			if strings.TrimSpace(part.Text) == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(part.Text)
		}
		if strings.TrimSpace(b.String()) != "" {
			return b.String()
		}
	}
	return ""
}

// SubAgentCompletionPrompt builds the inject prompt the Harness sends to the
// Main Agent when a sub-agent loop ends. It names the sub-agent id first so
// the Main Agent can read the final summary and send follow-ups.
func SubAgentCompletionPrompt(event SubAgentEvent) string {
	stepLabel := "investigation"
	if event.PlanStep.Index > 0 {
		stepLabel = fmt.Sprintf("plan step %d", event.PlanStep.Index)
	}
	return fmt.Sprintf("sub agent id %s finished with status %s for %s. Read its final summary with subagent_history, then continue. If the work is incomplete, send a follow-up message into its session with send_to_subagent. Result: %s", event.JobID, event.Status, stepLabel, event.Result)
}

func (m *subAgentManager) SetEventSink(sink func(SubAgentEvent)) {
	m.eventMu.Lock()
	m.sink = sink
	m.eventMu.Unlock()
}

func formatPlan(plan PlanState) string {
	var b strings.Builder
	for _, step := range plan.Steps {
		b.WriteString(fmt.Sprintf("%d. %s\n", step.Index, step.Text))
	}
	return strings.TrimSpace(b.String())
}

func (m *subAgentManager) Stop(id string) bool {
	m.mu.RLock()
	job := m.jobs[id]
	cancel := func() {}
	if job != nil && job.cancel != nil {
		cancel = job.cancel
	}
	m.mu.RUnlock()
	if job == nil || job.cancel == nil {
		return false
	}
	cancel()
	return true
}

type subAgentRunner struct {
	manager *subAgentManager
	parent  *Session
}

func (r *subAgentRunner) Delegate(ctx context.Context, task string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return r.manager.Delegate(r.parent, task)
}
func (r *subAgentRunner) Status(id string) string  { return r.manager.Status(id) }
func (r *subAgentRunner) History(id string) string { return r.manager.History(id) }
func (r *subAgentRunner) Send(id, message string) (string, error) {
	return r.manager.Send(id, message)
}
func (r *subAgentRunner) Stop(id string) bool      { return r.manager.Stop(id) }

type subAgentTool struct{ runner SubAgentRunner }

func (t *subAgentTool) Definitions() []Tool {
	return []Tool{
		{Name: "delegate_to_subagent", Description: "Start a background worker task in an isolated session. Returns immediately with a job id; when the worker loop ends the Harness injects a prompt naming the sub agent id so the Main Agent can read its final summary.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"task": map[string]any{"type": "string"}}, "required": []string{"task"}}},
		{Name: "subagent_status", Description: "Check progress or result of a background sub-agent job.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}}, "required": []string{"job_id"}}},
		{Name: "subagent_history", Description: "Read the final summary and execution history of a sub-agent job without reading repository source directly.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}}, "required": []string{"job_id"}}},
		{Name: "send_to_subagent", Description: "Send a follow-up message into a finished sub-agent session when its work is incomplete. The worker continues from its session state and reports completion again.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"}}, "required": []string{"job_id", "message"}}},
		{Name: "stop_subagent", Description: "Stop a running background sub-agent job when the user changes direction or the task should be cancelled.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}}, "required": []string{"job_id"}}},
	}
}
func (t *subAgentTool) Execute(ctx context.Context, call ToolCall) ToolResult {
	result := ToolResult{ID: call.ID}
	var input struct {
		Task    string `json:"task"`
		JobID   string `json:"job_id"`
		Message string `json:"message"`
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
	case "send_to_subagent":
		id, err := t.runner.Send(strings.TrimSpace(input.JobID), input.Message)
		if err != nil {
			result.Content = err.Error()
			result.IsError = true
			return result
		}
		result.Content = "follow-up sent to sub-agent: " + id + "; wait for its completion prompt, then read the new final summary"
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
