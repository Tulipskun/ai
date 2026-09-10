//go:build linux

package cli

import "golang.org/x/sys/unix"

func getTermiosOS(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TCGETS)
}

func setTermiosOS(fd int, term *unix.Termios) error {
	return unix.IoctlSetTermios(fd, unix.TCSETS, term)
}
