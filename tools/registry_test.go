package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func TestRegistryDefinitions(t *testing.T) {
	r, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defs := r.Definitions()
	if len(defs) != 25 {
		t.Fatalf("definitions=%d", len(defs))
	}
	seen := map[string]bool{}
	for _, d := range defs {
		seen[d.Name] = true
	}
	for _, name := range []string{"read_file", "read_files", "write_file", "edit_file", "list_directory", "search_files", "bash", "run_job", "check_job", "close_job", "web_fetch", "list_attachments", "read_attachment", "describe_attachment", "send_attachment", "os_mouse_move", "os_mouse_click", "os_key_press", "os_type_text", "os_screenshot", "os_mouse_drag", "os_mouse_scroll", "os_window_list", "os_window_focus", "os_window_geometry"} {
		if !seen[name] {
			t.Fatalf("missing %s", name)
		}
	}
}

func TestRegistryBrowserDefinitions(t *testing.T) {
	browser := NewBrowserClient(BrowserClientConfig{})
	r, err := NewRegistryWithBrowser(t.TempDir(), browser, true, "")
	if err != nil {
		t.Fatal(err)
	}
	defs := r.Definitions()
	if len(defs) != 38 {
		t.Fatalf("definitions=%d", len(defs))
	}
	seen := map[string]bool{}
	for _, d := range defs {
		seen[d.Name] = true
	}
	for _, name := range []string{"web_fetch", "browser_list_pages", "browser_attach", "browser_open", "browser_close", "browser_navigate", "browser_snapshot", "browser_click", "browser_fill", "browser_press", "browser_select", "browser_scroll", "browser_get_text", "browser_screenshot"} {
		if !seen[name] {
			t.Fatalf("missing %s", name)
		}
	}
}

func TestRegistryUnknownTool(t *testing.T) {
	r, _ := NewRegistry(t.TempDir())
	res := r.Execute(context.Background(), sdk.ToolCall{ID: "1", Name: "missing", Arguments: `{}`})
	if !res.IsError {
		t.Fatal("expected unknown tool error")
	}
}

func TestRunCommandDescriptionWarnsAboutDirectExec(t *testing.T) {
	r, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range r.Definitions() {
		if d.Name != "bash" {
			continue
		}
		for _, want := range []string{"bash command line", "chains", "pipes", "exit code"} {
			if !strings.Contains(d.Description, want) {
				t.Fatalf("bash description missing %q: %s", want, d.Description)
			}
		}
		return
	}
	t.Fatal("bash definition missing")
}

func TestWorkspaceResolverIsolatesSessionRoots(t *testing.T) {
	global := t.TempDir()
	sessionWorkspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(global, "shared.txt"), []byte("global file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionWorkspace, "session.txt"), []byte("session file"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := NewRegistry(global)
	if err != nil {
		t.Fatal(err)
	}
	r.SetWorkspaceResolver(func(ctx context.Context) string {
		if sdk.SessionIDFromContext(ctx) == "s2" {
			return sessionWorkspace
		}
		return ""
	})
	call := func(sessionID, name string, args any) sdk.ToolResult {
		raw, _ := json.Marshal(args)
		ctx := sdk.WithSessionID(context.Background(), sessionID)
		return r.Execute(ctx, sdk.ToolCall{ID: "x", Name: name, Arguments: string(raw)})
	}
	if result := call("s1", "read_file", map[string]string{"path": "shared.txt"}); result.IsError {
		t.Fatalf("global session lost global file: %s", result.Content)
	}
	if result := call("s2", "read_file", map[string]string{"path": "session.txt"}); result.IsError || result.Content != "session file" {
		t.Fatalf("session workspace not applied: %+v", result)
	}
	if result := call("s2", "read_file", map[string]string{"path": "shared.txt"}); !result.IsError {
		t.Fatal("session workspace still sees the global file")
	}
	bash := call("s2", "bash", map[string]string{"command": "pwd"})
	if bash.IsError || !strings.Contains(bash.Content, strings.TrimPrefix(sessionWorkspace, "/")) {
		t.Fatalf("bash cwd not session workspace: %+v", bash)
	}
	bashGlobal := call("s1", "bash", map[string]string{"command": "pwd"})
	if bashGlobal.IsError || !strings.Contains(bashGlobal.Content, strings.TrimPrefix(global, "/")) {
		t.Fatalf("bash cwd not global workspace: %+v", bashGlobal)
	}
}
