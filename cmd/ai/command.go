package main

import (
	"errors"
	"fmt"
	"io"
)

type command int

const (
	commandStart command = iota
)

func parseCommand(args []string) (command, error) {
	if len(args) == 0 || args[0] == "start" {
		if len(args) > 1 {
			return commandStart, fmt.Errorf("start: unexpected argument %q", args[1])
		}
		return commandStart, nil
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		return commandStart, errHelp
	}
	return commandStart, fmt.Errorf("unknown command %q", args[0])
}

var errHelp = errors.New("help requested")

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: ai <command>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  start    Start the AI Harness")
}
