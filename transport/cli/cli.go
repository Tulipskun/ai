package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Tulipskun/ai/sdk"
)

const Source = "cli"

// InputSource turns newline-delimited stdin into canonical Harness inputs.
type InputSource struct {
	In        io.Reader
	Out       io.Writer
	SessionID string
}

func New(in io.Reader, out io.Writer) *InputSource {
	return &InputSource{In: in, Out: out, SessionID: "cli:default"}
}

func (s *InputSource) Receive(ctx context.Context) (<-chan sdk.Input, error) {
	if s == nil || s.In == nil {
		return nil, fmt.Errorf("cli: input reader is required")
	}
	id := s.SessionID
	if id == "" {
		id = "cli:default"
	}
	out := make(chan sdk.Input)
	go func() {
		defer close(out)
		scanner := bufio.NewScanner(s.In)
		for {
			if err := ctx.Err(); err != nil {
				return
			}
			if !scanner.Scan() {
				return
			}
			text := strings.TrimSpace(scanner.Text())
			if text == "" {
				continue
			}
			select {
			case out <- sdk.Input{Source: Source, SessionID: id, Turn: sdk.Turn{Role: sdk.RoleUser, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: text}}}}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// Display writes completed assistant responses to the terminal.
type Display struct {
	Out io.Writer
}

func NewDisplay(out io.Writer) *Display {
	return &Display{Out: out}
}

func (d *Display) Source() string { return Source }

func (d *Display) Display(_ context.Context, output sdk.Output) error {
	if d == nil || d.Out == nil {
		return fmt.Errorf("cli: output writer is required")
	}
	for _, part := range output.Content {
		if part.Type == sdk.ContentText && part.Text != "" {
			if _, err := fmt.Fprintln(d.Out, part.Text); err != nil {
				return err
			}
		}
	}
	return nil
}
