package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func runUninstall() error {
	app, err := installedBinary()
	if err != nil {
		return err
	}
	state, err := stateRoot()
	if err != nil {
		return err
	}

	if daemonRunning() {
		if err := stopDaemon(app); err != nil {
			return fmt.Errorf("stop daemon: %w", err)
		}
	}

	// Remove the invoked path itself. This also removes a legacy symlink
	// without following it.
	if err := os.Remove(app); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove binary %s: %w", app, err)
	}

	// Older installations stored the executable under the state directory.
	// Remove that exact legacy path only; never recursively follow arbitrary
	// symlinks outside the state directory.
	legacyBinary := filepath.Join(state, "ai")
	if legacyBinary != app {
		if info, err := os.Lstat(legacyBinary); err == nil {
			if info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				if err := os.Remove(legacyBinary); err != nil {
					return fmt.Errorf("remove legacy binary: %w", err)
				}
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}

	if err := os.RemoveAll(state); err != nil {
		return fmt.Errorf("remove runtime state %s: %w", state, err)
	}
	fmt.Printf("[ai] uninstalled %s\n", app)
	fmt.Printf("[ai] removed runtime state %s\n", state)
	return nil
}
