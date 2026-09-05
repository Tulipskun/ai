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
		{name: "no args keeps backwards compatibility", args: nil, want: commandStart},
		{name: "help", args: []string{"help"}, want: commandStart, err: errHelp},
		{name: "short help", args: []string{"-h"}, want: commandStart, err: errHelp},
		{name: "long help", args: []string{"--help"}, want: commandStart, err: errHelp},
		{name: "unknown command", args: []string{"status"}, want: commandStart},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCommand(tt.args)
			if got != tt.want {
				t.Fatalf("command = %v, want %v", got, tt.want)
			}
			if !errors.Is(err, tt.err) {
				t.Fatalf("error = %v, want %v", err, tt.err)
			}
		})
	}
}
