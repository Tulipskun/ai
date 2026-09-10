package tools

import (
	"context"
	"net"
	"net/url"
	"testing"
)

func TestNetworkPolicyRejectsBlockedLiteralIPs(t *testing.T) {
	policy := NewNetworkPolicy(false)
	for _, raw := range []string{
		"http://127.0.0.1/",
		"http://10.0.0.1/",
		"http://172.16.0.1/",
		"http://192.168.1.1/",
		"http://169.254.169.254/",
		"http://[::1]/",
		"http://[fc00::1]/",
		"http://[fe80::1]/",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := policy.ValidateURL(context.Background(), u); err == nil {
			t.Errorf("ValidateURL(%q) = nil, want blocked", raw)
		}
	}
}

func TestNetworkPolicyRejectsNonHTTP(t *testing.T) {
	policy := NewNetworkPolicy(false)
	for _, raw := range []string{"file:///tmp/test", "ftp://example.com/file", "javascript:alert(1)"} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := policy.ValidateURL(context.Background(), u); err == nil {
			t.Errorf("ValidateURL(%q) = nil, want rejected", raw)
		}
	}
}

func TestNetworkPolicyAllowsPublicLiteralIP(t *testing.T) {
	policy := NewNetworkPolicy(false)
	u, err := url.Parse("https://1.1.1.1/")
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.ValidateURL(context.Background(), u); err != nil {
		t.Fatalf("ValidateURL(public IP) = %v", err)
	}
}

func TestNetworkPolicyAllowsPrivateWhenEnabled(t *testing.T) {
	policy := NewNetworkPolicy(true)
	u, err := url.Parse("http://127.0.0.1/")
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.ValidateURL(context.Background(), u); err != nil {
		t.Fatalf("ValidateURL(private, allow=true) = %v", err)
	}
}

func TestNetworkPolicyResolverBlocksPrivateHostname(t *testing.T) {
	policy := NewNetworkPolicy(false)
	policy.LookupIPAddr = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.7")}}, nil
	}
	u, err := url.Parse("https://example.test/")
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.ValidateURL(context.Background(), u); err == nil {
		t.Fatal("ValidateURL(resolved private hostname) = nil, want blocked")
	}
}
