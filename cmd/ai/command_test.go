package main

import (
	"errors"
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want command
		err  error
	}{
		{name: "start", args: []string{"start"}, want: commandStart},
		{name: "cli", args: []string{"cli"}, want: commandCLI},
		{name: "discord", args: []string{"discord"}, want: commandDiscord},
		{name: "discord disable", args: []string{"discord", "disable"}, want: commandDiscord},
		{name: "browser", args: []string{"browser"}, want: commandBrowser},
		{name: "browser disable", args: []string{"browser", "disable"}, want: commandBrowser},
		{name: "update", args: []string{"update"}, want: commandUpdate},
		{name: "update pins version", args: []string{"update", "v1.35"}, want: commandUpdate},
		{name: "update auto flag", args: []string{"update", "--auto"}, want: commandUpdate},
		{name: "update auto plus version", args: []string{"update", "--auto", "v1.35"}, want: commandUpdate},
		{name: "update rejects extras", args: []string{"update", "v1.35", "x"}, want: commandUpdate, err: errors.New("update usage")},
		{name: "uninstall", args: []string{"uninstall"}, want: commandUninstall},
		{name: "no args keeps backwards compatibility", args: nil, want: commandStart},
		{name: "help", args: []string{"help"}, want: commandStart, err: errHelp},
		{name: "short help", args: []string{"-h"}, want: commandStart, err: errHelp},
		{name: "long help", args: []string{"--help"}, want: commandStart, err: errHelp},
		{name: "unknown command", args: []string{"status"}, want: commandStart, err: errors.New("unknown command")},
		{name: "discord rejects flags", args: []string{"discord", "--token", "x"}, want: commandDiscord, err: errors.New("discord usage")},
		{name: "browser rejects flags", args: []string{"browser", "--mode", "x"}, want: commandBrowser, err: errors.New("browser usage")},
		{name: "system", args: []string{"system"}, want: commandSystem},
		{name: "system set", args: []string{"system", "set", "hi"}, want: commandSystem},
		{name: "system clear", args: []string{"system", "clear"}, want: commandSystem},
		{name: "system set needs text", args: []string{"system", "set"}, want: commandSystem, err: errors.New("system usage")},
		{name: "system rejects verbs", args: []string{"system", "drop"}, want: commandSystem, err: errors.New("system usage")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCommand(tt.args)
			if got != tt.want {
				t.Fatalf("command = %v, want %v", got, tt.want)
			}
			if tt.err != nil && err == nil {
				t.Fatal("expected an error")
			}
			if tt.err == nil && err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
			if errors.Is(tt.err, errHelp) && !errors.Is(err, errHelp) {
				t.Fatalf("error = %v, want errHelp", err)
			}
		})
	}
}
