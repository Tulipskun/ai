//go:build darwin

package cli

import "golang.org/x/sys/unix"

func getTermiosOS(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TIOCGETA)
}

func setTermiosOS(fd int, term *unix.Termios) error {
	return unix.IoctlSetTermios(fd, unix.TIOCSETA, term)
}
