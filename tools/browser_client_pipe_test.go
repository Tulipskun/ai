package tools

import (
	"bufio"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestChromiumPipeArgsNoTCPPort(t *testing.T) {
	args := chromiumPipeArgs("data/browser/profile", false)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--remote-debugging-pipe") {
		t.Fatalf("pipe args missing --remote-debugging-pipe: %v", args)
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--remote-debugging-port") || strings.HasPrefix(a, "--remote-debugging-address") {
			t.Fatalf("pipe args must not open a TCP port, got %q in %v", a, args)
		}
	}
}

func TestPipeFrameRoundTrip(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil { t.Fatal(err) }
	defer r.Close()
	defer w.Close()
	payload := []byte(`{"id":1,"method":"Target.getTargets"}`)
	frame := append(append([]byte{}, payload...), 0)
	go func() { _, _ = writeFullPipe(w, frame) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := make(chan []byte, 1)
	errch := make(chan error, 1)
	go func() {
		// exercise via client pipe fields
		c := &BrowserClient{pipeMode: true, pipeIn: w, pipeOut: r, pipeReader: nil}
		_ = c
		b, err := readPipeFrame(ctx, bufio.NewReader(r), 5*time.Second)
		if err != nil { errch <- err; return }
		got <- b
	}()
	select {
	case b := <-got:
		if string(b) != string(payload) { t.Fatalf("frame = %q, want %q", b, payload) }
	case err := <-errch:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("pipe frame read timed out")
	}
}
