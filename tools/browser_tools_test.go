package tools

import (
	"context"
	"encoding/json"
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
	for _, want := range []string{"session_id", "full_page", "required"} {
		if !strings.Contains(text, want) { t.Fatalf("schema missing %q: %s", want, text) }
	}
}

func TestBrowserNavigateNormalizesURL(t *testing.T) {
	_, err := normalizeURL("example.com")
	if err == nil { t.Fatal("expected URL validation error") }
	if _, err := normalizeURL("https://example.com"); err != nil { t.Fatal(err) }
}

func TestBrowserToolNilClient(t *testing.T) {
	tool := newBrowserTool(nil, "browser.open", decodeJSON[browserSessionArgs])
	_, err := tool(context.Background(), mustRawJSON(t, browserSessionArgs{SessionID:"s"}))
	if err == nil || !strings.Contains(err.Error(), "browser worker is unavailable") { t.Fatalf("error=%v", err) }
}
