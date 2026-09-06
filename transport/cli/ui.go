package cli

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Tulipskun/ai/sdk"
)

type UI struct {
	Out           io.Writer
	ShowToolTrace bool

	mu          sync.Mutex
	streaming   bool
	streamBytes int
}

func NewUI(out io.Writer) *UI {
	return &UI{Out: out, ShowToolTrace: true}
}

func (u *UI) Header(sessionID, provider, model string) {
	if u == nil || u.Out == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	fmt.Fprintln(u.Out, "AI Harness CLI")
	fmt.Fprintf(u.Out, "session  %s\n", shortID(sessionID))
	fmt.Fprintf(u.Out, "model    %s\n", modelLabel(provider, model))
	fmt.Fprintln(u.Out, "type /help for commands · Ctrl+C cancels a turn · Ctrl+D exits")
}

func (u *UI) Trace(event sdk.TraceEvent) {
	if u == nil || u.Out == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	switch event.Stage {
	case sdk.TraceResponseText:
		if !u.streaming {
			u.streaming = true
			u.streamBytes = 0
			fmt.Fprint(u.Out, "ai › ")
		}
		fmt.Fprint(u.Out, event.Text)
		u.streamBytes += len(event.Text)
	case sdk.TraceResponse:
		if u.streaming {
			return
		}
		text := responseText(event.Response)
		if text != "" {
			fmt.Fprintf(u.Out, "ai › %s", text)
		}
	case sdk.TraceToolCall:
		if !u.ShowToolTrace || event.ToolCall == nil {
			return
		}
		u.endStreamLocked()
		fmt.Fprintf(u.Out, "tool › %s %s\n", event.ToolCall.Name, compact(event.ToolCall.Arguments, 420))
	case sdk.TraceToolResult:
		if !u.ShowToolTrace || event.ToolResult == nil {
			return
		}
		u.endStreamLocked()
		state := "ok"
		if event.ToolResult.IsError {
			state = "error"
		}
		fmt.Fprintf(u.Out, "tool › %s · %s\n", state, compact(event.ToolResult.Content, 420))
	case sdk.TraceRetryWait:
		u.endStreamLocked()
		if event.RetryAfter > 0 {
			fmt.Fprintf(u.Out, "retry › %s in %s\n", compactError(event.Err), formatDuration(event.RetryAfter))
		} else {
			fmt.Fprintf(u.Out, "retry › %s\n", compactError(event.Err))
		}
	}
}

func (u *UI) EndTurn() {
	if u == nil || u.Out == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.endStreamLocked()
}

func (u *UI) Print(text string) {
	if u == nil || u.Out == nil || strings.TrimSpace(text) == "" {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	fmt.Fprintln(u.Out, text)
}

func (u *UI) endStreamLocked() {
	if u.streaming {
		fmt.Fprintln(u.Out)
		u.streaming = false
		u.streamBytes = 0
	}
}

func responseText(response *sdk.Response) string {
	if response == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range response.Content {
		if part.Type == sdk.ContentText {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

func compact(text string, max int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if max <= 0 || len(runes) <= max {
		return text
	}
	return string(runes[:max-1]) + "…"
}

func compactError(err error) string {
	if err == nil {
		return "temporary failure"
	}
	return compact(err.Error(), 220)
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 16 {
		return id
	}
	return id[len(id)-16:]
}

func modelLabel(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" && model == "" {
		return "not configured"
	}
	if provider == "" {
		return model
	}
	if model == "" {
		return provider + "/<auto>"
	}
	return provider + "/" + model
}
