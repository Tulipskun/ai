package sdk

import "testing"

func TestRouterResolvesLogicalProviderAndModelToAdapter(t *testing.T) {
	r := NewRouter()
	r.Register(ModelRoute{Provider: ProviderOpenRouter, Model: "gpt-5", Adapter: AdapterOpenAI})
	r.Register(ModelRoute{Provider: ProviderOpenRouter, Model: "gemini-3.5", Adapter: AdapterGemini})
	r.Register(ModelRoute{Provider: ProviderOpenCode, Model: "opus", Adapter: AdapterAnthropic})

	cases := []struct { provider ProviderID; model string; adapter AdapterID }{
		{ProviderOpenRouter, "gpt-5", AdapterOpenAI},
		{ProviderOpenRouter, "gemini-3.5", AdapterGemini},
		{ProviderOpenCode, "opus", AdapterAnthropic},
	}
	for _, tc := range cases {
		route, err := r.Resolve(tc.provider, tc.model)
		if err != nil { t.Fatalf("Resolve() error = %v", err) }
		if route.Adapter != tc.adapter { t.Fatalf("adapter = %q, want %q", route.Adapter, tc.adapter) }
	}
}

func TestRouterRequiresExplicitProvider(t *testing.T) {
	r := NewRouter()
	r.Register(ModelRoute{Provider: ProviderOpenRouter, Model: "gpt-5", Adapter: AdapterOpenAI})
	if _, err := r.Resolve("", "gpt-5"); err == nil { t.Fatal("Resolve() expected an error for empty provider") }
}

func TestSessionPinsKeyIndex(t *testing.T) {
	pool := NewKeyPool("key-1", "key-2", "key-3")
	s1 := NewSession(SessionConfig{ID: "s1", Provider: ProviderOpenRouter, Model: "gpt-5", KeyIndex: 0}, pool)
	s2 := NewSession(SessionConfig{ID: "s2", Provider: ProviderOpenRouter, Model: "gemini-3.5", KeyIndex: 1}, pool)
	k1, err := s1.APIKey()
	if err != nil || k1 != "key-1" { t.Fatalf("session 1 key = %q, err=%v", k1, err) }
	k2, err := s2.APIKey()
	if err != nil || k2 != "key-2" { t.Fatalf("session 2 key = %q, err=%v", k2, err) }
	current, err := pool.Current()
	if err != nil || current != "key-1" { t.Fatalf("pool current = %q, err=%v", current, err) }
}
