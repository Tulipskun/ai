package sdk

import (
	"context"
	"testing"
	"time"
)

type loopTestProvider struct{}

func (loopTestProvider) Name() string { return "test" }
func (loopTestProvider) Generate(context.Context, Request) (Response, error) {
	return Response{Content: []ContentPart{{Type: ContentText, Text: "response"}}}, nil
}
func (loopTestProvider) Stream(context.Context, Request) (<-chan Event, error) {
	ch := make(chan Event, 1)
	ch <- Event{Type: EventDone}
	close(ch)
	return ch, nil
}

func newLoopTestClient() *RouterClient {
	router := NewRouter()
	router.Register(ModelRoute{Provider: ProviderOpenRouter, Model: "model", Adapter: AdapterOpenAI})
	client := NewRouterClient(router)
	client.RegisterAdapter(AdapterOpenAI, loopTestProvider{})
	return client
}

func TestLoopCommitsSessionBeforeDisplay(t *testing.T) {
	keys := NewKeyPool("test-key")
	session := NewSession(SessionConfig{ID: "s1", Provider: ProviderOpenRouter, Model: "model", KeyIndex: 0}, keys)
	seen := make(chan []Turn, 1)
	display := DisplayFunc(func(_ context.Context, _ Output) error {
		seen <- session.History()
		return nil
	})

	loop := &HarnessLoop{
		Client: newLoopTestClient(),
		ResolveSession: func(context.Context, Input) (*Session, error) { return session, nil },
		Displays:       []Display{display},
		DisplayTimeout: time.Second,
	}
	input := Input{Source: "discord", SessionID: "s1", Turn: Turn{Role: RoleUser, Content: []ContentPart{{Type: ContentText, Text: "input"}}}}
	if err := loop.Handle(context.Background(), input); err != nil {
		t.Fatal(err)
	}

	select {
	case history := <-seen:
		if len(history) != 2 || history[0].Role != RoleUser || history[1].Role != RoleModel {
			t.Fatalf("display observed uncommitted history: %+v", history)
		}
	case <-time.After(time.Second):
		t.Fatal("display was not dispatched")
	}
}

func TestLoopContinuesWhenDisplayFails(t *testing.T) {
	keys := NewKeyPool("test-key")
	session := NewSession(SessionConfig{ID: "s1", Provider: ProviderOpenRouter, Model: "model", KeyIndex: 0}, keys)
	loop := &HarnessLoop{
		Client: newLoopTestClient(),
		ResolveSession: func(context.Context, Input) (*Session, error) { return session, nil },
		Displays: []Display{DisplayFunc(func(context.Context, Output) error { return context.Canceled })},
		DisplayTimeout: time.Second,
	}

	if err := loop.Handle(context.Background(), Input{SessionID: "s1", Turn: Turn{Role: RoleUser, Content: []ContentPart{{Type: ContentText, Text: "input"}}}}); err != nil {
		t.Fatal(err)
	}
	if got := session.History(); len(got) != 2 {
		t.Fatalf("display failure changed core processing: %+v", got)
	}
}
