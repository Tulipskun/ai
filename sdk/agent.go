package sdk

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrAgentMaxIterations = errors.New("sdk: agent reached maximum iterations")
	ErrAgentRetriesExhausted = errors.New("sdk: agent retries exhausted")
)

// ToolExecutor provides the model-visible tool definitions and executes tool calls.
type ToolExecutor interface {
	Definitions() []Tool
	Execute(context.Context, ToolCall) ToolResult
}

// Agent runs the model/tool control loop independently of any transport.
type Agent struct {
	Client        *RouterClient
	Tools         ToolExecutor
	MaxIterations int
	MaxRetries    int
}

const (
	defaultAgentMaxIterations = 20
	defaultAgentMaxRetries    = 2
)

// RunTurn appends the user turn, repeatedly calls the model, executes returned
// tools, and stops when the model returns no tool calls or the iteration limit
// is reached. Agent-level failures restore the history to the state before the
// turn and retry the complete turn up to MaxRetries times. Tool failures are
// returned to the model as tool results and do not trigger an agent retry.
func (a *Agent) RunTurn(ctx context.Context, session *Session, user Turn, req Request) (Response, error) {
	if a == nil || a.Client == nil || session == nil {
		return Response{}, errors.New("sdk: incomplete agent configuration")
	}

	before := session.History()
	limit := a.MaxIterations
	if limit <= 0 {
		limit = defaultAgentMaxIterations
	}
	retries := a.MaxRetries
	if retries < 0 {
		retries = 0
	}
	if a.MaxRetries == 0 {
		retries = defaultAgentMaxRetries
	}

	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		session.ReplaceHistory(before)
		resp, err := a.runAttempt(ctx, session, user, req, limit)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		session.ReplaceHistory(before)
		if !retryableAgentError(ctx, err) || attempt == retries {
			break
		}
	}

	return Response{}, fmt.Errorf("%w: attempts=%d: %v", ErrAgentRetriesExhausted, retries+1, lastErr)
}

func (a *Agent) runAttempt(ctx context.Context, session *Session, user Turn, req Request, limit int) (Response, error) {
	session.Append(user)

	for iteration := 0; iteration < limit; iteration++ {
		req.Messages = session.History()
		if a.Tools != nil {
			req.Tools = a.Tools.Definitions()
		}

		resp, err := a.Client.Generate(ctx, session, req)
		if err != nil {
			return Response{}, err
		}
		commitResponse(session, resp)

		if len(resp.ToolCalls) == 0 {
			return resp, nil
		}
		if a.Tools == nil {
			for _, call := range resp.ToolCalls {
				session.Append(Turn{Role: RoleToolResult, ToolResult: &ToolResult{
					ID: call.ID, Content: "tool execution is not configured", IsError: true,
				}})
			}
			continue
		}
		for _, call := range resp.ToolCalls {
			result := a.Tools.Execute(ctx, call)
			if result.ID == "" {
				result.ID = call.ID
			}
			resultCopy := result
			session.Append(Turn{Role: RoleToolResult, ToolResult: &resultCopy})
		}
	}

	return Response{}, fmt.Errorf("%w: limit=%d", ErrAgentMaxIterations, limit)
}

func retryableAgentError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
