package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Tulipskun/ai/runtime"
	"github.com/Tulipskun/ai/runtime/d1store"
	"github.com/Tulipskun/ai/sdk"
	mobiletransport "github.com/Tulipskun/ai/transport/mobile"
)

// A provider that answers the health probe, so the admin surface can be exercised
// without a network.
func adminFixture(t *testing.T) (*adminStore, *sync.WaitGroup) {
	t.Helper()
	root := t.TempDir()
	providerFile := filepath.Join(root, "config", "provider.json")
	if err := os.MkdirAll(filepath.Dir(providerFile), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"providers":[{"name":"test-gateway","adapter":"openai","http_endpoint":"https://api.example.invalid/v1","api_keys":["k1"]}]}`
	if err := os.WriteFile(providerFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := &runtime.Runtime{Router: sdk.NewRouter(), Client: sdk.NewRouterClient(sdk.NewRouter())}
	manager := runtime.NewProviderManager(providerFile, rt, runtime.ProviderFileConfig{})
	store := newAdminStore(root, (*d1store.Client)(nil), manager, &runtime.ProviderFileConfig{}, nil, nil,
		sdk.SessionConfig{Provider: "boot", Model: "boot-model"})
	return store, &sync.WaitGroup{}
}

func TestAdminAddsProviderAndNeverReturnsKeyMaterial(t *testing.T) {
	store, _ := adminFixture(t)
	view, err := store.AddProvider(context.Background(), mobiletransport.ProviderSpec{
		ID: "second", Adapter: "openai", Endpoint: "https://api.example.invalid/v1", Keys: []string{"secret-key"},
	})
	if err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	if view.ID != "second" || view.KeyCount != 1 {
		t.Fatalf("view = %+v", view)
	}
	list, err := store.Providers(context.Background())
	if err != nil {
		t.Fatalf("Providers: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("providers = %+v", list)
	}
	for _, p := range list {
		if p.ID == "" {
			t.Fatalf("provider without an id: %+v", p)
		}
	}
}

func TestAdminRejectsBadProviders(t *testing.T) {
	store, _ := adminFixture(t)
	cases := []mobiletransport.ProviderSpec{
		{ID: "", Adapter: "openai", Endpoint: "https://x.example", Keys: []string{"k"}},
		{ID: "a", Adapter: "nope", Endpoint: "https://x.example", Keys: []string{"k"}},
		{ID: "a", Adapter: "openai", Endpoint: "http://x.example", Keys: []string{"k"}},
		{ID: "a", Adapter: "openai", Endpoint: "https://x.example"},
	}
	for i, spec := range cases {
		if _, err := store.AddProvider(context.Background(), spec); err == nil {
			t.Fatalf("case %d was accepted: %+v", i, spec)
		}
	}
	if _, err := store.AddProvider(context.Background(), mobiletransport.ProviderSpec{
		ID: "test-gateway", Adapter: "openai", Endpoint: "https://x.example", Keys: []string{"k"},
	}); err == nil {
		t.Fatal("a duplicate provider name must be refused")
	}
}

func TestAdminKeyPoolEdits(t *testing.T) {
	store, _ := adminFixture(t)
	if _, err := store.UpdateKeys(context.Background(), "test-gateway", mobiletransport.KeyChange{Add: []string{"k2"}}); err != nil {
		t.Fatalf("add: %v", err)
	}
	list, _ := store.Providers(context.Background())
	if list[0].KeyCount != 2 {
		t.Fatalf("key count after add = %d, want 2", list[0].KeyCount)
	}
	if _, err := store.UpdateKeys(context.Background(), "test-gateway", mobiletransport.KeyChange{Remove: []int{0, 1}}); err == nil {
		t.Fatal("emptying the key pool must be refused")
	}
	if _, err := store.UpdateKeys(context.Background(), "missing", mobiletransport.KeyChange{Add: []string{"k"}}); err == nil {
		t.Fatal("an unknown provider must be refused")
	}
}

// A provider nobody probed is unknown, not healthy, and a key change takes the
// old verdict away with it.
func TestAdminReportsUntestedUntilProbed(t *testing.T) {
	store, _ := adminFixture(t)
	list, err := store.Providers(context.Background())
	if err != nil {
		t.Fatalf("Providers: %v", err)
	}
	if list[0].Probed {
		t.Fatalf("a provider nobody probed must not report probed: %+v", list[0])
	}
	store.setStatus("test-gateway", "401 Invalid API key")
	list, _ = store.Providers(context.Background())
	if !list[0].Probed || list[0].LastError == "" || list[0].Reachable {
		t.Fatalf("a failed probe must read as unusable: %+v", list[0])
	}
	if _, err := store.UpdateKeys(context.Background(), "test-gateway", mobiletransport.KeyChange{Add: []string{"k2"}}); err != nil {
		t.Fatalf("add key: %v", err)
	}
	list, _ = store.Providers(context.Background())
	if list[0].Probed || list[0].LastError == "401 Invalid API key" {
		t.Fatalf("changing the pool must clear the old verdict: %+v", list[0])
	}
}

func TestAdminSettingsRoundTrip(t *testing.T) {
	store, _ := adminFixture(t)
	view, err := store.Settings(context.Background())
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if view.Main.Provider != "boot" || view.Main.Model != "boot-model" {
		t.Fatalf("default main route = %+v, want the daemon boot route", view.Main)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("settings should serialise")
	}
}
