package sdk

func (a *Agent) SetSubAgentTraceSink(sink func(SubAgentEvent)) {
	if a == nil {
		return
	}
	a.subAgentMu.Lock()
	manager := a.subAgents
	if manager == nil {
		manager = newSubAgentManager(a, a.SubAgentConfig)
		a.subAgents = manager
	}
	a.subAgentMu.Unlock()
	manager.SetTraceSink(sink)
}
