//go:build linux || darwin

package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

func enableRawTerminal(f *os.File) (func(), error) {
	fd := int(f.Fd())
	term, err := getTermios(fd)
	if err != nil {
		return nil, err
	}
	raw := *term
	raw.Iflag &^= unix.IXON | unix.IXOFF | unix.ICRNL
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := setTermios(fd, &raw); err != nil {
		return nil, err
	}
	return func() { _ = setTermios(fd, term) }, nil
}

// getTermios/setTermios are split by OS because ioctl request constants differ.
func getTermios(fd int) (*unix.Termios, error) { return getTermiosOS(fd) }
func setTermios(fd int, term *unix.Termios) error { return setTermiosOS(fd, term) }
