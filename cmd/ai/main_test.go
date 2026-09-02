package main

import (
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func TestNewAgentWithTools(t *testing.T) {
	client := sdk.NewRouterClient(sdk.NewRouter())
	agent, err := newAgent(client, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if agent == nil {
		t.Fatal("agent is nil")
	}
	if agent.Client != client {
		t.Fatal("agent client was not wired")
	}
	if agent.Tools == nil {
		t.Fatal("agent tools are not wired")
	}
	if len(agent.Tools.Definitions()) == 0 {
		t.Fatal("agent has no tool definitions")
	}
}
