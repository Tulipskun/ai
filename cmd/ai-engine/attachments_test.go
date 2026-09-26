package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tulipskun/ai-engine/runtime"
	"github.com/Tulipskun/ai-engine/runtime/filestore"
	"github.com/Tulipskun/ai-engine/sdk"
	"github.com/Tulipskun/ai-engine/tools"
)

// These tests cover only the wiring: config in, one store out, and that store
// reaching the worker registry and the Discord transport. Nothing here opens a
// network connection, calls a provider, or talks to Discord.

// storeForTest opens a real attachment store under a temp state root through the
// same entry point the runtime uses.
func storeForTest(t *testing.T) (*runtime.AttachmentConfig, *filestore.Store, string) {
	t.Helper()
	state := t.TempDir()
	cfg, err := runtime.LoadAttachmentConfig(filepath.Join(state, runtime.DefaultAttachmentConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	store, err := cfg.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		t.Fatal("the default config must open a store")
	}
	return &cfg, store, state
}

func TestAttachmentRuntimeWiringReachesTheRegistry(t *testing.T) {
	_, store, state := storeForTest(t)
	toolStore := attachmentToolStore(store)
	if toolStore == nil {
		t.Fatal("an opened store must reach the worker registry")
	}
	ref, err := toolStore.PutWithContentType(context.Background(), "mobile:session:1", "design.txt", "", bytes.NewReader([]byte("plan text")))
	if err != nil {
		t.Fatal(err)
	}
	reader, meta, err := toolStore.Get(context.Background(), "mobile:session:1", ref.ID)
	if err != nil {
		t.Fatalf("the registry cannot read back what it stored: %v", err)
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "plan text" || meta.Name != "design.txt" {
		t.Fatalf("meta=%+v body=%q", meta, body)
	}
	// And the store still lives under the state root, not the working tree.
	if !strings.HasPrefix(store.Root(), state) {
		t.Fatalf("store root %q is outside the state root %q", store.Root(), state)
	}
}

func TestNewAgentInjectsTheAttachmentStore(t *testing.T) {
	client := sdk.NewRouterClient(sdk.NewRouter())
	_, store, state := storeForTest(t)
	jobsPath := filepath.Join(state, "data", "jobs.json")
	agent, err := newAgent(client, t.TempDir(), nil, false, jobsPath, store)
	if err != nil {
		t.Fatal(err)
	}
	if agent == nil || agent.Tools == nil {
		t.Fatal("agent wiring is incomplete")
	}
	registry, ok := agent.Tools.(*tools.Registry)
	if !ok {
		t.Fatalf("agent tools = %T, want the worker registry", agent.Tools)
	}
	for _, name := range []string{"list_attachments", "read_attachment", "describe_attachment"} {
		found := false
		for _, def := range registry.Definitions() {
			if def.Name == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("worker tool %s is missing from the registry", name)
		}
	}
	// The injected store is really the one the tools use: a file the transport
	// side put there is listed for the same session, by reference only.
	const session = "discord:channel:agent"
	if _, err := store.Put(context.Background(), session, "design.txt", bytes.NewReader([]byte("plan text"))); err != nil {
		t.Fatal(err)
	}
	result := agent.Tools.Execute(sdk.WithSessionID(context.Background(), session), sdk.ToolCall{ID: "1", Name: "list_attachments", Arguments: "{}"})
	if result.IsError {
		t.Fatalf("list_attachments: %s", result.Content)
	}
	if !strings.Contains(result.Content, "design.txt") {
		t.Fatalf("list result = %q", result.Content)
	}
	if strings.Contains(result.Content, "plan text") {
		t.Fatalf("attachment content entered a tool result: %q", result.Content)
	}
	read := agent.Tools.Execute(sdk.WithSessionID(context.Background(), session), sdk.ToolCall{ID: "2", Name: "read_attachment", Arguments: `{"ref_id":"` + designRef(t, store, session) + `"}`})
	if read.IsError {
		t.Fatalf("read_attachment: %s", read.Content)
	}
}

// designRef returns the stored reference ID of one session's attachment.
func designRef(t *testing.T, store *filestore.Store, session string) string {
	t.Helper()
	metas, err := store.List(context.Background(), session)
	if err != nil || len(metas) == 0 {
		t.Fatalf("list seeded attachment: %v", err)
	}
	return metas[0].ID
}

func TestNewAgentWithoutStoreReportsDisabled(t *testing.T) {
	client := sdk.NewRouterClient(sdk.NewRouter())
	agent, err := newAgent(client, t.TempDir(), nil, false, filepath.Join(t.TempDir(), "data", "jobs.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	result := agent.Tools.Execute(sdk.WithSessionID(context.Background(), "discord:channel:none"), sdk.ToolCall{ID: "1", Name: "list_attachments", Arguments: "{}"})
	if !result.IsError || !strings.Contains(result.Content, "not configured") {
		t.Fatalf("disabled store result = %+v", result)
	}
}

func TestAttachmentToolsReachOnlyTheWorker(t *testing.T) {
	// The Main Agent must be offered no attachment tool, while a delegated
	// worker is offered all three: the same registry is the worker's executor,
	// and planning filters the Main Agent's set by name (REQ-016, REQ-017).
	state := t.TempDir()
	_, store, _ := storeForTest(t)
	workspace := t.TempDir()
	router := sdk.NewRouter()
	keys := sdk.NewKeyPool("test-key")
	router.RegisterProvider(sdk.ProviderConfig{ID: "test", BaseURL: "http://test", Keys: keys, Adapter: sdk.AdapterOpenAI})
	router.Register(sdk.ModelRoute{Provider: "test", Model: "model", Adapter: sdk.AdapterOpenAI})
	provider := &roleSplitProvider{main: make(chan sdk.Request, 2), worker: make(chan sdk.Request, 2)}
	client := sdk.NewRouterClient(router)
	client.RegisterAdapter(sdk.AdapterOpenAI, provider)
	agent, err := newAgent(client, workspace, nil, false, filepath.Join(state, "data", "jobs.json"), store)
	if err != nil {
		t.Fatal(err)
	}
	session, err := sdk.OpenSession(filepath.Join(state, "parent.db"), sdk.SessionConfig{ID: "parent", Provider: "test", Model: "model"}, keys)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err := agent.RunTurn(context.Background(), session, sdk.Turn{Role: sdk.RoleUser, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "inspect the attachment"}}}, sdk.Request{SystemPrompt: "inspect and delegate"}); err != nil {
		t.Fatal(err)
	}
	mainTools := toolNames(<-provider.main)
	if mainTools == "" {
		t.Fatal("the Main Agent was given no tools at all")
	}
	for _, name := range []string{"list_attachments", "read_attachment", "describe_attachment"} {
		if strings.Contains(mainTools, name) {
			t.Fatalf("the Main Agent was offered %s: %s", name, mainTools)
		}
	}
	workerRequest, ok := receiveWithin(provider.worker, 10*time.Second)
	if !ok {
		t.Fatal("no delegated worker request was captured")
	}
	workerTools := toolNames(workerRequest)
	for _, name := range []string{"list_attachments", "read_attachment", "describe_attachment"} {
		if !strings.Contains(workerTools, name) {
			t.Fatalf("the worker was not offered %s: %s", name, workerTools)
		}
	}
}

// roleSplitProvider records the tool sets of the two roles. The Main Agent
// delegates once and then finishes with the worker's report, so one turn
// produces a request for each role with no network traffic.
type roleSplitProvider struct {
	main   chan sdk.Request
	worker chan sdk.Request
}

func (p *roleSplitProvider) Name() string { return "test" }

func (p *roleSplitProvider) WithAPIKey(string) sdk.Provider { return p }

func (p *roleSplitProvider) Generate(_ context.Context, req sdk.Request) (sdk.Response, error) {
	const delegateID = "delegate"
	call := sdk.ToolCall{ID: delegateID, Name: "delegate_to_subagent", Arguments: `{"task":"read the attachment and report it"}`}
	if !strings.Contains(req.SystemPrompt, "You are the Main Agent") {
		select {
		case p.worker <- req:
		default:
		}
		return sdk.Response{Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "worker report"}}}, nil
	}
	for _, turn := range req.Messages {
		if turn.ToolResult != nil && turn.ToolResult.ID == delegateID {
			return sdk.Response{Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: turn.ToolResult.Content}}}, nil
		}
	}
	select {
	case p.main <- req:
	default:
	}
	return sdk.Response{ToolCalls: []sdk.ToolCall{call}}, nil
}

func (p *roleSplitProvider) Stream(ctx context.Context, req sdk.Request) (<-chan sdk.Event, error) {
	resp, err := p.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	ch := make(chan sdk.Event, 1)
	ch <- sdk.Event{Type: sdk.EventDone, Response: &resp}
	close(ch)
	return ch, nil
}

func toolNames(req sdk.Request) string {
	names := make([]string, 0, len(req.Tools))
	for _, tool := range req.Tools {
		names = append(names, tool.Name)
	}
	return strings.Join(names, ",")
}

func receiveWithin(ch chan sdk.Request, wait time.Duration) (sdk.Request, bool) {
	select {
	case req := <-ch:
		return req, true
	case <-time.After(wait):
		return sdk.Request{}, false
	}
}

// fakeCleaner records sweep calls so the cleanup loop is testable without a
// filesystem, and can report an error the way a real store does.
type fakeCleaner struct {
	calls int
	now   time.Time
	err   error
}

func (f *fakeCleaner) Cleanup(_ context.Context, now time.Time) (int, error) {
	f.calls++
	f.now = now
	if f.err != nil {
		return 0, f.err
	}
	return 1, nil
}

func TestSweepAttachmentsToleratesErrors(t *testing.T) {
	cleaner := &fakeCleaner{}
	sweepAttachments(context.Background(), cleaner)
	if cleaner.calls != 1 || cleaner.now.IsZero() {
		t.Fatalf("cleaner = %+v", cleaner)
	}
	// An error is logged, not returned: the caller must keep sweeping.
	failing := &fakeCleaner{err: errors.New("disk busy")}
	sweepAttachments(context.Background(), failing)
	if failing.calls != 1 {
		t.Fatalf("failed sweep did not run: %+v", failing)
	}
}

func TestRunAttachmentCleanupSweepsAndStops(t *testing.T) {
	cleaner := &fakeCleaner{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runAttachmentCleanup(ctx, cleaner, 5*time.Millisecond)
		close(done)
	}()
	deadline := time.After(2 * time.Second)
	for cleaner.calls < 2 {
		select {
		case <-deadline:
			t.Fatalf("the sweep loop never repeated: %+v", cleaner)
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-deadline:
		t.Fatal("the sweep loop ignored cancellation")
	}
}

func TestStartAttachmentCleanupIgnoresNilStore(t *testing.T) {
	startAttachmentCleanup(context.Background(), nil, time.Millisecond)
}

func TestAttachmentCleanupRunsAgainstARealStore(t *testing.T) {
	state := t.TempDir()
	store, err := filestore.OpenUnder(state, filestore.DefaultRoot, filestore.Limits{MaxFileBytes: 1 << 20, MaxSessionBytes: 1 << 20, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(context.Background(), "discord:channel:ttl", "old.txt", bytes.NewReader([]byte("stale")))
	if err != nil {
		t.Fatal(err)
	}
	startAttachmentCleanup(context.Background(), store, time.Millisecond)
	removed, err := store.Cleanup(context.Background(), time.Now().UTC().Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d", removed)
	}
	if _, _, err := store.Get(context.Background(), "discord:channel:ttl", ref.ID); !errors.Is(err, filestore.ErrNotFound) {
		t.Fatalf("expired attachment survived: %v", err)
	}
}
