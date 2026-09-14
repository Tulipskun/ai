package sdk

func (a *Agent) SetSubAgentSinks(completion func(SubAgentEvent), trace func(SubAgentEvent)) {
	if a == nil {
		return
	}
	manager := a.subAgentManager()
	manager.SetEventSink(func(event SubAgentEvent) {
		if event.Trace != nil {
			if trace != nil {
				trace(event)
			}
			return
		}
		if completion != nil {
			completion(event)
		}
	})
}
