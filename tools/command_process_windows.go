//go:build windows

package tools

import "os/exec"

func configureCommandProcess(_ *exec.Cmd) {}

func killCommandProcessTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
