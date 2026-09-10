package tools

import (
	"context"
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func TestRegistryDefinitions(t *testing.T) {
	r, err := NewRegistry(t.TempDir())
	if err != nil { t.Fatal(err) }
	defs := r.Definitions()
	if len(defs) != 10 { t.Fatalf("definitions=%d", len(defs)) }
	seen := map[string]bool{}
	for _, d := range defs { seen[d.Name] = true }
	for _, name := range []string{"read_file", "write_file", "edit_file", "list_directory", "search_files", "run_command", "run_job", "check_job", "close_job", "web_fetch"} { if !seen[name] { t.Fatalf("missing %s", name) } }
}

func TestRegistryBrowserDefinitions(t *testing.T) {
	browser := NewBrowserClient(BrowserClientConfig{})
	r, err := NewRegistryWithBrowser(t.TempDir(), browser, true, "")
	if err != nil { t.Fatal(err) }
	defs := r.Definitions()
	if len(defs) != 23 { t.Fatalf("definitions=%d", len(defs)) }
	seen := map[string]bool{}
	for _, d := range defs { seen[d.Name] = true }
	for _, name := range []string{"web_fetch", "browser_list_pages", "browser_attach", "browser_open", "browser_close", "browser_navigate", "browser_snapshot", "browser_click", "browser_fill", "browser_press", "browser_select", "browser_scroll", "browser_get_text", "browser_screenshot"} { if !seen[name] { t.Fatalf("missing %s", name) } }
}

func TestRegistryUnknownTool(t *testing.T) {
	r, _ := NewRegistry(t.TempDir())
	res := r.Execute(context.Background(), sdk.ToolCall{ID:"1",Name:"missing",Arguments:`{}`})
	if !res.IsError { t.Fatal("expected unknown tool error") }
}
