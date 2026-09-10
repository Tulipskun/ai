package tools

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestBrowserToolDecodeArguments(t *testing.T) {
	raw := mustRawJSON(t, browserFillArgs{SessionID:"s", Ref:"e1", Text:"hello"})
	value, err := decodeJSON[browserFillArgs](raw)
	if err != nil { t.Fatal(err) }
	args := value.(browserFillArgs)
	if args.SessionID != "s" || args.Ref != "e1" || args.Text != "hello" { t.Fatalf("args=%#v", args) }
}

func TestBrowserSchemaShape(t *testing.T) {
	schema := browserSchema(map[string]any{"session_id":stringProperty(), "full_page":boolProperty()}, []string{"session_id"})
	data, err := json.Marshal(schema)
	if err != nil { t.Fatal(err) }
	text := string(data)
	for _, want := range []string{"session_id", "full_page", "required"} { if !strings.Contains(text, want) { t.Fatalf("schema missing %q: %s", want, text) } }
}

func TestBrowserNavigateRejectsUnsupportedScheme(t *testing.T) {
	u, err := url.Parse("ftp://example.com")
	if err != nil { t.Fatal(err) }
	policy := NewNetworkPolicy(true)
	if err := policy.ValidateURL(context.Background(), u); err == nil { t.Fatal("expected unsupported scheme error") }
}

func TestBrowserToolNilClient(t *testing.T) {
	tool := newBrowserTool(nil, "browser.open", decodeJSON[browserSessionArgs])
	_, err := tool(context.Background(), mustRawJSON(t, browserSessionArgs{SessionID:"s"}))
	if err == nil || !strings.Contains(err.Error(), "browser is unavailable") { t.Fatalf("error=%v", err) }
}
