package sdk

// SetSubAgentTraceSink installs the live trace renderer for worker jobs.
// Completion is delivered inline by the blocking delegation calls, so there
// is no completion sink anymore (REQ-021, REQ-034).
func (a *Agent) SetSubAgentTraceSink(trace func(SubAgentEvent)) {
	if a == nil {
		return
	}
	a.subAgentMu.Lock()
	a.subAgentTraceSink = trace
	manager := a.subAgents
	a.subAgentMu.Unlock()
	if manager != nil {
		manager.SetTraceSink(trace)
	}
}
