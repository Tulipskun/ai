package internal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func DoJSON(ctx context.Context, client *http.Client, method, url string, headers map[string]string, body any, out any) error {
	data, err := json.Marshal(body)
	if err != nil { return err }
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(data))
	if err != nil { return err }
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers { req.Header.Set(k, v) }
	resp, err := client.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("http %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func SSE(ctx context.Context, client *http.Client, method, url string, headers map[string]string, body any, onData func([]byte) error) error {
	data, err := json.Marshal(body)
	if err != nil { return err }
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(data))
	if err != nil { return err }
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers { req.Header.Set(k, v) }
	resp, err := client.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("http %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	s := bufio.NewScanner(resp.Body)
	s.Buffer(make([]byte, 4096), 4<<20)
	for s.Scan() {
		line := s.Bytes()
		if len(line) == 0 || !bytes.HasPrefix(line, []byte("data:")) { continue }
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if bytes.Equal(payload, []byte("[DONE]")) { continue }
		if err := onData(payload); err != nil { return err }
	}
	return s.Err()
}
