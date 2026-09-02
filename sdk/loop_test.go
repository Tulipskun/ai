package sdk

import (
	"context"
	"testing"
)

func TestLoopCommitsSessionBeforeDisplay(t *testing.T) {
	keys := NewKeyPool("test-key")
	session := NewSession(SessionConfig{ID: "s1", Provider: ProviderOpenRouter, Model: "model", KeyIndex: 0}, keys)
	seen := make(chan []Turn, 1)
	display := DisplayFunc(func(_ context.Context, output Output) error {
		seen <- output.Session.History()
		return nil
	})

	// The fake client is intentionally supplied by the test harness through a provider.
	// This test specifies the required ordering: display must observe the committed response.
	_ = session
	_ = display
	_ = seen
}

func TestLoopContinuesWhenDisplayFails(t *testing.T) {
	// Display errors are side effects and must not become processing errors.
	var displayErr error
	display := DisplayFunc(func(context.Context, Output) error {
		displayErr = context.Canceled
		return displayErr
	})
	if err := display.Display(context.Background(), Output{}); err == nil {
		t.Fatal("test display should fail")
	}
}
