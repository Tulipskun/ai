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
		{name: "update", args: []string{"update"}, want: commandUpdate},
		{name: "no args keeps backwards compatibility", args: nil, want: commandStart},
		{name: "help", args: []string{"help"}, want: commandStart, err: errHelp},
		{name: "short help", args: []string{"-h"}, want: commandStart, err: errHelp},
		{name: "long help", args: []string{"--help"}, want: commandStart, err: errHelp},
		{name: "unknown command", args: []string{"status"}, want: commandStart, err: errors.New("unknown command")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCommand(tt.args)
			if got != tt.want { t.Fatalf("command = %v, want %v", got, tt.want) }
			if tt.err != nil && err == nil { t.Fatal("expected an error") }
			if tt.err == nil && err != nil { t.Fatalf("error = %v, want nil", err) }
			if errors.Is(tt.err, errHelp) && !errors.Is(err, errHelp) { t.Fatalf("error = %v, want errHelp", err) }
		})
	}
}
