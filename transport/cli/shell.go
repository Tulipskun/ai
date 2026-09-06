package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

type CommandHandler func(context.Context, string) (output string, exit bool, err error)

type Shell struct {
	Editor  *LineEditor
	Out     io.Writer
	Handle  CommandHandler
	Header  func()
}

func (s *Shell) Run(ctx context.Context) error {
	if s == nil || s.Editor == nil || s.Out == nil || s.Handle == nil {
		return errors.New("cli: shell is not configured")
	}
	if s.Header != nil {
		s.Header()
	}
	for {
		line, err := s.Editor.ReadLine(ctx.Done())
		if errors.Is(err, ErrInterrupt) {
			continue
		}
		if errors.Is(err, ErrEOF) {
			fmt.Fprintln(s.Out, "bye")
			return nil
		}
		if err != nil {
			return err
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		output, exit, err := s.Handle(ctx, line)
		if err != nil {
			fmt.Fprintf(s.Out, "[error] %v\n", err)
			continue
		}
		if output != "" {
			fmt.Fprintln(s.Out, output)
		}
		if exit {
			fmt.Fprintln(s.Out, "bye")
			return nil
		}
	}
}
