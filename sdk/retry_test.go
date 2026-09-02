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

func TestRetryDelayUsesRetryAfterAndCapsAt60Seconds(t *testing.T) {
	if got := retryDelay(retryTestError{status: 429, delay: 7 * time.Second}, 1); got != 7*time.Second {
		t.Fatalf("delay=%s", got)
	}
	if got := retryDelay(retryTestError{status: 429, delay: 90 * time.Second}, 1); got != 60*time.Second {
		t.Fatalf("delay=%s", got)
	}
}

func TestRetryDelayFallsBackToBoundedBackoff(t *testing.T) {
	if got := retryDelay(errors.New("temporary"), 1); got != 250*time.Millisecond {
		t.Fatalf("attempt 1 delay=%s", got)
	}
	if got := retryDelay(errors.New("temporary"), 3); got != time.Second {
		t.Fatalf("attempt 3 delay=%s", got)
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
