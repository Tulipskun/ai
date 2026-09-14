package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type HTTPError struct {
	StatusCode int
	Status     string
	Body       string
	RetryAfterDelay time.Duration
}

func (e *HTTPError) Error() string { return fmt.Sprintf("http %s: %s", e.Status, e.Body) }
func (e *HTTPError) HTTPStatusCode() int { return e.StatusCode }
func (e *HTTPError) RetryAfter() time.Duration { return e.RetryAfterDelay }

func StatusCode(err error) (int, bool) {
	var he *HTTPError
	if errors.As(err, &he) { return he.StatusCode, true }
	return 0, false
}

func retryAfter(resp *http.Response) time.Duration {
	value := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if value == "" { return 0 }
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if d := time.Until(when); d > 0 { return d }
	}
	return 0
}

func DoJSON(ctx context.Context, client *http.Client, method, url string, headers map[string]string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil { return err }
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil { return err }
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers { req.Header.Set(k, v) }
	resp, err := client.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status, Body: strings.TrimSpace(string(b)), RetryAfterDelay: retryAfter(resp)}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
