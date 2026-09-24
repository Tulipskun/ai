package mobile

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var trycloudflareURL = regexp.MustCompile(`https://[A-Za-z0-9.-]+\.trycloudflare\.com`)

func portOfListen(listen string) int {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return 18789
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 {
		return 18789
	}
	return n
}

// RunQuickTunnel publishes a localhost listener through a Cloudflare quick
// tunnel — no account, no port forwarding. The public hostname is random, which
// is the first authentication step; the D1 token in the handshake header is the
// second (REQ-046(2)).
//
// --metrics 127.0.0.1:0 is deliberate: some hosts cannot resolve the hostname
// "localhost", and cloudflared then aborts before the tunnel is registered
// (Cloudflare error 1033).
func RunQuickTunnel(ctx context.Context, port int, binary string) (string, func(), error) {
	if binary == "" {
		binary = "cloudflared"
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return "", nil, fmt.Errorf("mobile: cloudflared not found in PATH: %w", err)
	}
	child, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(child, path, "tunnel", "--no-autoupdate",
		"--metrics", "127.0.0.1:0", "--url", fmt.Sprintf("http://127.0.0.1:%d", port))
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return "", nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return "", nil, err
	}
	found := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 64*1024), 64*1024)
		for scanner.Scan() {
			if match := trycloudflareURL.FindString(scanner.Text()); match != "" {
				select {
				case found <- match:
				default:
				}
			}
		}
	}()
	var public string
	select {
	case public = <-found:
	case <-time.After(45 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		cancel()
		return "", nil, errors.New("mobile: timed out waiting for the trycloudflare URL")
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		cancel()
		return "", nil, ctx.Err()
	}
	return public, func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		cancel()
	}, nil
}

// AnnounceTunnel tells the Worker the public URL so a phone can discover this
// daemon with GET /api/node instead of a hardcoded host. It runs with the token
// the phone handed over; there is no daemon credential of its own (CON-012).
func AnnounceTunnel(ctx context.Context, workerBase, token, publicURL, version string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("mobile: no D1 token yet (waiting for a phone)")
	}
	if publicURL == "" || !trycloudflareURL.MatchString(publicURL) {
		return fmt.Errorf("mobile: %q is not a trycloudflare URL", publicURL)
	}
	body, err := json.Marshal(map[string]string{"tunnel_url": publicURL, "version": version})
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(workerBase, "/") + "/api/node/heartbeat"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ai")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("mobile: announce tunnel -> HTTP %d", resp.StatusCode)
	}
	return nil
}

// DiscoverTunnel resolves the daemon's public URL from the Worker, used by the
// phone side and by operators checking the pairing.
func DiscoverTunnel(ctx context.Context, workerBase, token string) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(workerBase, "/")+"/api/node", nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "ai")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", false, errors.New("mobile: token rejected by Worker")
	}
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("mobile: discover -> HTTP %d", resp.StatusCode)
	}
	var payload struct {
		TunnelURL string `json:"tunnel_url"`
		Online    bool   `json:"online"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", false, err
	}
	if payload.TunnelURL == "" {
		return "", false, nil
	}
	return payload.TunnelURL, payload.Online, nil
}

// WSURL converts a public tunnel URL into the WebSocket endpoint the phone
// dials.
func WSURL(tunnelURL string) (string, error) {
	parsed, err := url.Parse(tunnelURL)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "https" {
		return "", fmt.Errorf("mobile: expected an https tunnel URL, got %q", tunnelURL)
	}
	return "wss://" + parsed.Host + "/ws", nil
}

var _ = log.Printf
