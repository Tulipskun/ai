//go:build !linux && !darwin

package cli

import (
	"errors"
	"os"
)

func enableRawTerminal(*os.File) (func(), error) {
	return func() {}, errors.New("interactive line editing is only supported on Linux and macOS")
}
