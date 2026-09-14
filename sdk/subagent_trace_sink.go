package sdk

func (a *Agent) SetSubAgentSinks(completion func(SubAgentEvent), trace func(SubAgentEvent)) {
	if a == nil {
		return
	}
	manager := a.subAgentManager()
	manager.SetEventSink(completion)
	manager.SetTraceSink(trace)
}
