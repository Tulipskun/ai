package tools

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type NetworkPolicy struct {
	AllowPrivate   bool
	LookupIPAddr   func(context.Context, string) ([]net.IPAddr, error)
	DialTimeout    time.Duration
}

func NewNetworkPolicy(allowPrivate bool) *NetworkPolicy {
	return &NetworkPolicy{AllowPrivate: allowPrivate, LookupIPAddr: net.DefaultResolver.LookupIPAddr, DialTimeout: 30 * time.Second}
}

func (p *NetworkPolicy) ValidateURL(ctx context.Context, u *url.URL) error {
	if p == nil {
		return errors.New("network policy is nil")
	}
	if u == nil {
		return errors.New("URL is nil")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	if u.User != nil {
		return errors.New("URL userinfo is not allowed")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("URL hostname is required")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !p.AllowPrivate && isBlockedIP(ip) {
			return fmt.Errorf("network access denied to %s", ip)
		}
		return nil
	}
	lookup := p.LookupIPAddr
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("resolve %q: no addresses", host)
	}
	if !p.AllowPrivate {
		for _, addr := range ips {
			if isBlockedIP(addr.IP) {
				return fmt.Errorf("network access denied to %q (%s)", host, addr.IP)
			}
		}
	}
	return nil
}

func (p *NetworkPolicy) ValidateAndRoundTrip(ctx context.Context, client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	} else {
		transport = transport.Clone()
	}
	baseDialer := &net.Dialer{Timeout: p.DialTimeout}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := p.lookup(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !p.AllowPrivate && isBlockedIP(ip) {
				continue
			}
			conn, err := baseDialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, fmt.Errorf("network access denied or connection failed for %q", host)
	}
	client.Transport = transport
	return client
}

func (p *NetworkPolicy) lookup(ctx context.Context, host string) ([]net.IPAddr, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IPAddr{{IP: ip}}, nil
	}
	lookup := p.LookupIPAddr
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	return lookup(ctx, host)
}

func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	ip = ip.To16()
	if ip == nil {
		return true
	}
	blocked := []struct{ network string }{
		{"127.0.0.0/8"},
		{"10.0.0.0/8"},
		{"172.16.0.0/12"},
		{"192.168.0.0/16"},
		{"169.254.0.0/16"},
		{"::1/128"},
		{"fc00::/7"},
		{"fe80::/10"},
	}
	for _, item := range blocked {
		_, network, err := net.ParseCIDR(item.network)
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func normalizeURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	return u, nil
}
