package sdk

import (
	"context"
	"errors"
	"testing"
	"time"
)

type retryTestError struct {
	status int
	delay  time.Duration
}

func (e retryTestError) Error() string { return "rate limited" }
func (e retryTestError) HTTPStatusCode() int { return e.status }
func (e retryTestError) RetryAfter() time.Duration { return e.delay }

func TestRetryDelayUsesProviderRetryAfter(t *testing.T) {
	if got := retryDelay(retryTestError{status: 429, delay: 5 * time.Second}, 1); got != 5*time.Second {
		t.Fatalf("provider Retry-After delay=%s, want 5s", got)
	}
	if got := retryDelay(retryTestError{status: 429, delay: 120 * time.Second}, 1); got != 96*time.Second {
		t.Fatalf("provider Retry-After delay=%s, want 96s cap", got)
	}
}

func TestRetryDelayFallsBackToDeterministicSchedule(t *testing.T) {
	want := []time.Duration{
		3 * time.Second,
		6 * time.Second,
		12 * time.Second,
		24 * time.Second,
		48 * time.Second,
		96 * time.Second,
		96 * time.Second,
	}
	for attempt, expected := range want {
		if got := retryDelay(retryTestError{status: 429}, attempt+1); got != expected {
			t.Fatalf("attempt %d delay=%s, want %s", attempt+1, got, expected)
		}
	}
}

func TestRateLimitDetection(t *testing.T) {
	if !isRateLimitError(retryTestError{status: 429}) {
		t.Fatal("429 was not detected")
	}
	if isRateLimitError(retryTestError{status: 500}) {
		t.Fatal("500 was incorrectly treated as rate limit")
	}
}

func TestWaitRetryHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitRetry(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
