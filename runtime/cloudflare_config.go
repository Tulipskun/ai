package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultCloudflareConfigPath = "config/cloudflare.json"

type CloudflareConfig struct {
	Endpoint     string
	ComputeToken string
	PollInterval string
	Lease        string
	Timeout      string
}

func LoadCloudflareConfig(path string) (CloudflareConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) { return CloudflareConfig{}, nil }
		return CloudflareConfig{}, fmt.Errorf("runtime: read cloudflare config: %w", err)
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil { return CloudflareConfig{}, fmt.Errorf("runtime: decode cloudflare config: %w", err) }
	cfg := CloudflareConfig{Endpoint: raw["endpoint"], ComputeToken: raw["compute_token"], PollInterval: raw["poll_interval"], Lease: raw["lease"], Timeout: raw["timeout"]}
	cfg.Endpoint = strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	cfg.ComputeToken = strings.TrimSpace(cfg.ComputeToken)
	if cfg.Endpoint == "" || cfg.ComputeToken == "" { return CloudflareConfig{}, errors.New("runtime: cloudflare endpoint and compute_token are required") }
	if _, _, _, err := cfg.Durations(); err != nil { return CloudflareConfig{}, err }
	return cfg, nil
}
func SaveCloudflareConfig(path string, cfg CloudflareConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { return err }
	raw := map[string]string{"endpoint": cfg.Endpoint, "compute_token": cfg.ComputeToken}
	if cfg.PollInterval != "" { raw["poll_interval"] = cfg.PollInterval }
	if cfg.Lease != "" { raw["lease"] = cfg.Lease }
	if cfg.Timeout != "" { raw["timeout"] = cfg.Timeout }
	data, err := json.MarshalIndent(raw, "", "  "); if err != nil { return err }
	return os.WriteFile(path, append(data, '\n'), 0600)
}
func (c CloudflareConfig) Durations() (poll, lease, timeout time.Duration, err error) {
	poll, lease, timeout = 2*time.Second, 30*time.Second, 15*time.Second
	parse := func(value string, def time.Duration) (time.Duration, error) { value=strings.TrimSpace(value); if value=="" { return def,nil }; d,e:=time.ParseDuration(value); if e!=nil || d<=0 { return 0,fmt.Errorf("runtime: invalid cloudflare duration %q",value) }; return d,nil }
	if poll,err=parse(c.PollInterval,poll); err!=nil { return }; if lease,err=parse(c.Lease,lease); err!=nil { return }; if timeout,err=parse(c.Timeout,timeout); err!=nil { return }; return
}
