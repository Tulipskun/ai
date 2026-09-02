package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type BrowserClientConfig struct {
	Host            string
	Port            int
	NodeCommand     string
	WorkerPath      string
	WorkerDir       string
	Headless        bool
	RPCTimeout      time.Duration
	StartupTimeout  time.Duration
}

type BrowserClient struct {
	cfg    BrowserClientConfig
	mu     sync.RWMutex
	baseURL string
	token  string
	cmd    *exec.Cmd
	ready  bool
	client *http.Client
}

func NewBrowserClient(config BrowserClientConfig) *BrowserClient {
	if config.Host == "" {
		config.Host = "127.0.0.1"
	}
	if config.NodeCommand == "" {
		config.NodeCommand = "node"
	}
	if config.RPCTimeout <= 0 {
		config.RPCTimeout = 30 * time.Second
	}
	if config.StartupTimeout <= 0 {
		config.StartupTimeout = 30 * time.Second
	}
	return &BrowserClient{cfg: config, client: &http.Client{Timeout: config.RPCTimeout}}
}

func NewBrowserClientForTest(baseURL, token string, config BrowserClientConfig) *BrowserClient {
	client := NewBrowserClient(config)
	client.baseURL = strings.TrimRight(baseURL, "/")
	client.token = token
	client.ready = true
	return client
}

func (c *BrowserClient) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.ready {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	if c.cfg.WorkerPath == "" {
		return errors.New("browser worker path is required")
	}

	startCtx, cancel := context.WithTimeout(ctx, c.cfg.StartupTimeout)
	defer cancel()
	args := []string{c.cfg.WorkerPath}
	if c.cfg.Headless {
		args = append(args, "--headless=true")
	} else {
		args = append(args, "--headless=false")
	}
	cmd := exec.CommandContext(startCtx, c.cfg.NodeCommand, args...)
	cmd.Dir = c.cfg.WorkerDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("browser worker stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("browser worker stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start browser worker: %w", err)
	}
	go io.Copy(io.Discard, stderr)

	lineCh := make(chan struct {
		line string
		err  error
	}, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			lineCh <- struct {
				line string
				err  error
			}{scanner.Text(), nil}
			return
		}
		if err := scanner.Err(); err != nil {
			lineCh <- struct {
				line string
				err  error
			}{"", err}
			return
		}
		lineCh <- struct {
			line string
				err  error
		}{"", io.EOF}
	}()

	select {
	case <-startCtx.Done():
		_ = cmd.Process.Kill()
		return startCtx.Err()
	case item := <-lineCh:
		if item.err != nil {
			_ = cmd.Process.Kill()
			return fmt.Errorf("browser worker startup: %w", item.err)
		}
		var ready struct {
			Ready bool   `json:"ready"`
			Host  string `json:"host"`
			Port  int    `json:"port"`
			Token string `json:"token"`
		}
		if err := json.Unmarshal([]byte(item.line), &ready); err != nil || !ready.Ready || ready.Port <= 0 || ready.Token == "" {
			_ = cmd.Process.Kill()
			return errors.New("invalid browser worker startup response")
		}
		host := ready.Host
		if host == "" {
			host = c.cfg.Host
		}
		c.mu.Lock()
		c.cmd = cmd
		c.baseURL = fmt.Sprintf("http://%s:%d", host, ready.Port)
		c.token = ready.Token
		c.ready = true
		c.mu.Unlock()
		return nil
	}
}

func (c *BrowserClient) Close() error {
	c.mu.Lock()
	cmd := c.cmd
	c.cmd = nil
	c.ready = false
	c.baseURL = ""
	c.token = ""
	c.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	return nil
}

func (c *BrowserClient) Ready() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ready
}

func (c *BrowserClient) Call(ctx context.Context, method string, params any, result any) error {
	c.mu.RLock()
	baseURL, token, ready := c.baseURL, c.token, c.ready
	client := c.client
	c.mu.RUnlock()
	if !ready || baseURL == "" || token == "" {
		return errors.New("browser worker is unavailable")
	}
	requestID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	payload := map[string]any{"id": requestID, "method": method, "params": params}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal browser RPC: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/rpc", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("browser RPC %s: %w", method, err)
	}
	defer response.Body.Close()
	var envelope struct {
		ID     string          `json:"id"`
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode browser RPC: %w", err)
	}
	if !envelope.OK {
		if envelope.Error != nil {
			err := errors.New(envelope.Error.Message)
			if envelope.Error.Code != "" {
				err = fmt.Errorf("%s: %w", envelope.Error.Code, err)
			}
			return err
		}
		return fmt.Errorf("browser RPC %s failed", method)
	}
	if result != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, result); err != nil {
			return fmt.Errorf("decode browser RPC result: %w", err)
		}
	}
	return nil
}
