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
	commandDiscord
	commandBrowser
	commandSystem
	commandStop
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
		if _, _, err := parseUpdateArgs(args[1:]); err != nil {
			return commandUpdate, err
		}
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
	if args[0] == "discord" {
		if len(args) > 2 || (len(args) == 2 && args[1] != "disable") {
			return commandDiscord, fmt.Errorf("discord: usage is 'ai discord' or 'ai discord disable'")
		}
		return commandDiscord, nil
	}
	if args[0] == "browser" {
		if len(args) > 2 || (len(args) == 2 && args[1] != "disable") {
			return commandBrowser, fmt.Errorf("browser: usage is 'ai browser' or 'ai browser disable'")
		}
		return commandBrowser, nil
	}
	if args[0] == "system" {
		if len(args) >= 2 && args[1] != "set" && args[1] != "clear" {
			return commandSystem, fmt.Errorf("system: usage is 'ai system', 'ai system set <prompt>' or 'ai system clear'")
		}
		if len(args) == 2 && args[1] == "set" {
			return commandSystem, fmt.Errorf("system: usage is 'ai system set <prompt>'")
		}
		return commandSystem, nil
	}
	if args[0] == "stop" {
		if len(args) > 1 {
			return commandStop, fmt.Errorf("stop: unexpected argument %q", args[1])
		}
		return commandStop, nil
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
	fmt.Fprintln(w, "  stop       Stop the running AI Harness daemon")
	fmt.Fprintln(w, "  cli        Open an interactive AI Harness CLI session")
	fmt.Fprintln(w, "  discord    Configure Discord interactively")
	fmt.Fprintln(w, "  browser    Configure browser automation interactively")
	fmt.Fprintln(w, "  system     Show or set the model system prompt")
	fmt.Fprintln(w, "  update [--auto] [version]  Download and replace the installed AI binary")
	fmt.Fprintln(w, "  uninstall  Stop AI and remove the binary and runtime state")
}
