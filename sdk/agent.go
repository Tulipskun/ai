package sdk

import (
	"context"
	"errors"
	"fmt"
)

var ErrAgentMaxIterations = errors.New("sdk: agent reached maximum iterations")

// ToolExecutor provides the model-visible tool definitions and executes tool calls.
type ToolExecutor interface {
	Definitions() []Tool
	Execute(context.Context, ToolCall) ToolResult
}

// Agent runs the model/tool control loop independently of any transport.
type Agent struct {
	Client         *RouterClient
	Tools          ToolExecutor
	MaxIterations  int
}

const defaultAgentMaxIterations = 20

// RunTurn appends the user turn, repeatedly calls the model, executes returned
// tools, and stops when the model returns no tool calls or the iteration limit
// is reached. A provider failure restores the history from before the turn.
func (a *Agent) RunTurn(ctx context.Context, session *Session, user Turn, req Request) (Response, error) {
	if a == nil || a.Client == nil || session == nil {
		return Response{}, errors.New("sdk: incomplete agent configuration")
	}
	before := session.History()
	session.Append(user)

	limit := a.MaxIterations
	if limit <= 0 {
		limit = defaultAgentMaxIterations
	}

	for iteration := 0; iteration < limit; iteration++ {
		req.Messages = session.History()
		if a.Tools != nil {
			req.Tools = a.Tools.Definitions()
		}

		resp, err := a.Client.Generate(ctx, session, req)
		if err != nil {
			session.ReplaceHistory(before)
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
