package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const defaultCommandTimeout = 30 * time.Second
const maxCommandOutputBytes = 1 << 20

type commandOutput struct { Output string `json:"output"`; ExitCode int `json:"exit_code"` }
type runCommandArgs struct { Command string `json:"command"`; Args []string `json:"args"`; TimeoutMS int `json:"timeout_ms"` }

func runCommandTool(workspace string) handler {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args runCommandArgs
		if err := json.Unmarshal(raw, &args); err != nil { return "", err }
		if args.Command == "" { return "", errors.New("command is required") }
		runCtx := ctx
		cancel := func() {}
		if args.TimeoutMS > 0 { runCtx, cancel = context.WithTimeout(ctx, time.Duration(args.TimeoutMS)*time.Millisecond) } else { runCtx, cancel = context.WithTimeout(ctx, defaultCommandTimeout) }
		defer cancel()
		cmd := exec.CommandContext(runCtx, args.Command, args.Args...); cmd.Dir = workspace
		var out limitedCommandBuffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		code := 0
		if err != nil { if exitErr, ok := err.(*exec.ExitError); ok { code = exitErr.ExitCode() } else { code = -1 } }
		result := commandOutput{Output: out.String(), ExitCode: code}
		data, marshalErr := json.Marshal(result); if marshalErr != nil { return "", marshalErr }
		if err != nil { if runCtx.Err() != nil { return string(data), fmt.Errorf("command %s: %w", args.Command, runCtx.Err()) }; return string(data), fmt.Errorf("command %s failed: %w", args.Command, err) }
		return string(data), nil
	}
}

type limitedCommandBuffer struct { data []byte }
func (b *limitedCommandBuffer) Write(p []byte) (int,error) { if len(b.data)<maxCommandOutputBytes { n:=len(p); if len(b.data)+n>maxCommandOutputBytes { n=maxCommandOutputBytes-len(b.data) }; b.data=append(b.data,p[:n]...) }; return len(p),nil }
func (b *limitedCommandBuffer) String() string { return strings.TrimSpace(string(b.data)) }

func parsePositiveInt(value string, fallback int) int { n,err:=strconv.Atoi(value); if err!=nil || n<=0{return fallback}; return n }
