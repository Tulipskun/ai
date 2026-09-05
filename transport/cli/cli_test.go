package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func TestInputSourceReceivesLines(t *testing.T) {
	source := New(strings.NewReader("hello\n\nworld\n"), nil)
	inputs, err := source.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []sdk.Input
	for input := range inputs {
		got = append(got, input)
	}
	if len(got) != 2 {
		t.Fatalf("got %d inputs, want 2", len(got))
	}
	if got[0].Source != Source || got[0].SessionID != "cli:default" || got[0].Turn.Content[0].Text != "hello" {
		t.Fatalf("unexpected first input: %+v", got[0])
	}
	if got[1].Turn.Content[0].Text != "world" {
		t.Fatalf("unexpected second input: %+v", got[1])
	}
}

func TestDisplayWritesText(t *testing.T) {
	var out strings.Builder
	display := NewDisplay(&out)
	if err := display.Display(context.Background(), sdk.Output{Source: Source, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "answer"}}}); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "answer\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
