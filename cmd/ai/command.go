package main

import (
	"errors"
	"fmt"
	"io"
)

type command int

const (
	commandStart command = iota
	commandCLI
	commandUpdate
	commandDaemon
	commandUninstall
)

func parseCommand(args []string) (command, error) {
	if len(args) == 0 || args[0] == "start" {
		if len(args) > 1 { return commandStart, fmt.Errorf("start: unexpected argument %q", args[1]) }
		return commandStart, nil
	}
	if args[0] == "cli" {
		if len(args) > 1 { return commandCLI, fmt.Errorf("cli: unexpected argument %q", args[1]) }
		return commandCLI, nil
	}
	if args[0] == "update" {
		if len(args) > 1 { return commandUpdate, fmt.Errorf("update: unexpected argument %q", args[1]) }
		return commandUpdate, nil
	}
	if args[0] == "uninstall" {
		if len(args) > 1 { return commandUninstall, fmt.Errorf("uninstall: unexpected argument %q", args[1]) }
		return commandUninstall, nil
	}
	if args[0] == "daemon" {
		if len(args) > 1 { return commandDaemon, fmt.Errorf("daemon: unexpected argument %q", args[1]) }
		return commandDaemon, nil
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
	fmt.Fprintln(w, "  start      Start the AI Harness in the background and return")
	fmt.Fprintln(w, "  cli        Open an interactive AI Harness CLI session")
	fmt.Fprintln(w, "  update     Download and replace the installed AI binary")
	fmt.Fprintln(w, "  uninstall  Stop AI and remove the binary and runtime state")
}
