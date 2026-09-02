package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type browserSessionArgs struct {
	SessionID string `json:"session_id"`
}

type browserNavigateArgs struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url"`
}

type browserRefArgs struct {
	SessionID string `json:"session_id"`
	Ref       string `json:"ref"`
}

type browserFillArgs struct {
	SessionID string `json:"session_id"`
	Ref       string `json:"ref"`
	Text      string `json:"text"`
}

type browserPressArgs struct {
	SessionID string `json:"session_id"`
	Ref       string `json:"ref"`
	Key       string `json:"key"`
}

type browserSelectArgs struct {
	SessionID string `json:"session_id"`
	Ref       string `json:"ref"`
	Value     string `json:"value"`
}

type browserScrollArgs struct {
	SessionID string `json:"session_id"`
	Direction string `json:"direction,omitempty"`
	Amount    int     `json:"amount,omitempty"`
}

type browserTextArgs struct {
	SessionID string `json:"session_id"`
	Ref       string `json:"ref,omitempty"`
}

type browserScreenshotArgs struct {
	SessionID string `json:"session_id"`
	FullPage  bool   `json:"full_page,omitempty"`
}

func newBrowserTool(client *BrowserClient, method string, decode func(json.RawMessage) (any, error)) handler {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		if client == nil {
			return "", errors.New("browser worker is unavailable")
		}
		params, err := decode(raw)
		if err != nil {
			return "", err
		}
		var result json.RawMessage
		if err := client.Call(ctx, method, params, &result); err != nil {
			return "", err
		}
		if len(result) == 0 || string(result) == "null" {
			return "{}", nil
		}
		return string(result), nil
	}
}

func decodeJSON[T any](raw json.RawMessage) (any, error) {
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("invalid browser arguments: %w", err)
	}
	return value, nil
}

func browserSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringProperty() map[string]any { return map[string]any{"type": "string"} }
func boolProperty() map[string]any   { return map[string]any{"type": "boolean"} }
func intProperty() map[string]any    { return map[string]any{"type": "integer"} }
