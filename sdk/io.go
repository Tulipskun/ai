package sdk

import (
	"context"
	"time"
)

// Input is the canonical event entering the Harness from any transport.
type Input struct {
	Source    string
	SessionID string
	Turn      Turn
	Metadata  map[string]string
}

// Output is the canonical result leaving the Harness for one or more displays.
type Output struct {
	Source    string
	SessionID string
	Content   []ContentPart
	Response  Response
	Metadata  map[string]string
}

type InputSource interface {
	Receive(context.Context) (<-chan Input, error)
}

type Display interface {
	Display(context.Context, Output) error
}

// RoutedDisplay is an optional extension for displays owned by a concrete
// transport. A routed display receives only outputs whose Source matches.
// Displays that do not implement RoutedDisplay retain the legacy broadcast
// behavior for compatibility.
type RoutedDisplay interface {
	Display
	Source() string
}

type DisplayFunc func(context.Context, Output) error

func (f DisplayFunc) Display(ctx context.Context, output Output) error { return f(ctx, output) }

const defaultDisplayTimeout = 10 * time.Second

// DispatchDisplay makes display a best-effort side effect. It never waits for
// the display implementation and recovers panics from that implementation.
// A timeout prevents a broken transport from leaving an unbounded goroutine.
func DispatchDisplay(parent context.Context, display Display, output Output, timeout time.Duration) {
	if display == nil {
		return
	}
	if routed, ok := display.(RoutedDisplay); ok {
		source := routed.Source()
		if source != "" && source != output.Source {
			return
		}
	}
	if timeout <= 0 {
		timeout = defaultDisplayTimeout
	}
	go func() {
		defer func() { _ = recover() }()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), timeout)
		defer cancel()
		_ = display.Display(ctx, output)
	}()
}
