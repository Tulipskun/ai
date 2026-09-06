package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

var (
	ErrInterrupt = errors.New("cli: interrupted")
	ErrEOF       = errors.New("cli: end of input")
)

type LineEditor struct {
	In          *os.File
	Out         io.Writer
	HistoryPath string
	Prompt      func() string

	history []string
}

func NewLineEditor(in *os.File, out io.Writer) *LineEditor {
	return &LineEditor{In: in, Out: out, Prompt: func() string { return "> " }}
}

func (e *LineEditor) LoadHistory() error {
	if e == nil || e.HistoryPath == "" {
		return nil
	}
	file, err := os.Open(e.HistoryPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line != "" && !strings.HasPrefix(line, "/provider add ") {
			e.history = append(e.history, line)
		}
	}
	e.trimHistory()
	return scanner.Err()
}

func (e *LineEditor) AppendHistory(line string) error {
	line = strings.TrimSpace(line)
	if e == nil || line == "" || strings.HasPrefix(line, "/provider add ") {
		return nil
	}
	if len(e.history) == 0 || e.history[len(e.history)-1] != line {
		e.history = append(e.history, line)
		e.trimHistory()
	}
	if e.HistoryPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(e.HistoryPath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(e.HistoryPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, line)
	return err
}

func (e *LineEditor) trimHistory() {
	const maxEntries = 500
	if len(e.history) > maxEntries {
		e.history = e.history[len(e.history)-maxEntries:]
	}
}

func (e *LineEditor) ReadLine(ctxDone <-chan struct{}) (string, error) {
	if e == nil || e.In == nil || e.Out == nil {
		return "", errors.New("cli: editor requires input and output")
	}
	prompt := ""
	if e.Prompt != nil {
		prompt = e.Prompt()
	}
	fmt.Fprint(e.Out, prompt)

	restore, err := enableRawTerminal(e.In)
	if err != nil {
		return e.readFallback(prompt, ctxDone)
	}
	defer restore()

	buffer := []rune{}
	cursor := 0
	historyIndex := len(e.history)

	redraw := func() {
		fmt.Fprint(e.Out, "\r\x1b[2K", prompt, string(buffer))
		if tail := len(buffer) - cursor; tail > 0 {
			fmt.Fprintf(e.Out, "\x1b[%dD", tail)
		}
	}

	for {
		select {
		case <-ctxDone:
			return "", errors.New("cli: context canceled")
		default:
		}
		key, err := readKey(e.In)
		if err != nil {
			return "", err
		}
		switch key.kind {
		case keyEnter:
			fmt.Fprint(e.Out, "\r\n")
			line := strings.TrimSpace(string(buffer))
			if line != "" {
				_ = e.AppendHistory(line)
			}
			return line, nil
		case keyInterrupt:
			fmt.Fprint(e.Out, "^C\r\n")
			return "", ErrInterrupt
		case keyEOF:
			fmt.Fprint(e.Out, "\r\n")
			return "", ErrEOF
		case keyBackspace:
			if cursor > 0 {
				buffer = append(buffer[:cursor-1], buffer[cursor:]...)
				cursor--
				redraw()
			}
		case keyDelete:
			if cursor < len(buffer) {
				buffer = append(buffer[:cursor], buffer[cursor+1:]...)
				redraw()
			}
		case keyLeft:
			if cursor > 0 {
				cursor--
				redraw()
			}
		case keyRight:
			if cursor < len(buffer) {
				cursor++
				redraw()
			}
		case keyHome:
			cursor = 0
			redraw()
		case keyEnd:
			cursor = len(buffer)
			redraw()
		case keyUp:
			if len(e.history) > 0 && historyIndex > 0 {
				historyIndex--
				buffer = []rune(e.history[historyIndex])
				cursor = len(buffer)
				redraw()
			}
		case keyDown:
			if historyIndex < len(e.history)-1 {
				historyIndex++
				buffer = []rune(e.history[historyIndex])
				cursor = len(buffer)
				redraw()
			} else if historyIndex == len(e.history)-1 {
				historyIndex = len(e.history)
				buffer = nil
				cursor = 0
				redraw()
			}
		case keyCtrlU:
			buffer = nil
			cursor = 0
			redraw()
		case keyCtrlK:
			buffer = buffer[:cursor]
			redraw()
		case keyText:
			buffer = append(buffer, 0)
			copy(buffer[cursor+1:], buffer[cursor:])
			buffer[cursor] = key.r
			cursor++
			redraw()
		}
	}
}

func (e *LineEditor) readFallback(prompt string, ctxDone <-chan struct{}) (string, error) {
	reader := bufio.NewReader(e.In)
	for {
		select {
		case <-ctxDone:
			return "", errors.New("cli: context canceled")
		default:
		}
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
		if line != "" {
			_ = e.AppendHistory(line)
			return line, nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", ErrEOF
			}
			return "", err
		}
		fmt.Fprint(e.Out, prompt)
	}
}

type keyKind int

const (
	keyText keyKind = iota
	keyEnter
	keyInterrupt
	keyEOF
	keyBackspace
	keyDelete
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyUp
	keyDown
	keyCtrlU
	keyCtrlK
)

type key struct {
	kind keyKind
	r    rune
}

func readKey(in *os.File) (key, error) {
	var first [1]byte
	if _, err := in.Read(first[:]); err != nil {
		return key{}, err
	}
	switch first[0] {
	case 3:
		return key{kind: keyInterrupt}, nil
	case 4:
		return key{kind: keyEOF}, nil
	case 9:
		return key{kind: keyText, r: '\t'}, nil
	case 10, 13:
		return key{kind: keyEnter}, nil
	case 8, 127:
		return key{kind: keyBackspace}, nil
	case 21:
		return key{kind: keyCtrlU}, nil
	case 11:
		return key{kind: keyCtrlK}, nil
	case 27:
		return readEscape(in)
	}

	if first[0] < utf8.RuneSelf {
		return key{kind: keyText, r: rune(first[0])}, nil
	}
	size := utf8RuneSize(first[0])
	buf := make([]byte, size)
	buf[0] = first[0]
	for i := 1; i < size; i++ {
		if _, err := in.Read(buf[i : i+1]); err != nil {
			return key{}, err
		}
	}
	r, decoded := utf8.DecodeRune(buf)
	if r == utf8.RuneError && decoded == 1 {
		return key{kind: keyText, r: rune(first[0])}, nil
	}
	return key{kind: keyText, r: r}, nil
}

func utf8RuneSize(b byte) int {
	switch {
	case b < 0xC0:
		return 1
	case b < 0xE0:
		return 2
	case b < 0xF0:
		return 3
	default:
		return 4
	}
}

func readEscape(in *os.File) (key, error) {
	var seq [2]byte
	if _, err := in.Read(seq[:1]); err != nil {
		return key{}, err
	}
	if seq[0] != '[' {
		return key{}, nil
	}
	if _, err := in.Read(seq[1:]); err != nil {
		return key{}, err
	}
	switch seq[1] {
	case 'A': return key{kind: keyUp}, nil
	case 'B': return key{kind: keyDown}, nil
	case 'C': return key{kind: keyRight}, nil
	case 'D': return key{kind: keyLeft}, nil
	case 'H': return key{kind: keyHome}, nil
	case 'F': return key{kind: keyEnd}, nil
	case '3':
		var tilde [1]byte
		if _, err := in.Read(tilde[:]); err != nil { return key{}, err }
		if tilde[0] == '~' { return key{kind: keyDelete}, nil }
	}
	return key{}, nil
}
