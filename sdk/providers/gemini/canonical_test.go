package gemini

import (
	"testing"

	"github.com/Tulipskun/ai/sdk"
	"github.com/Tulipskun/ai/sdk/providers/openai"
)

func TestBuildFromOpenAIMatchesBuild(t *testing.T) {
	temp := 0.5
	req := sdk.Request{Model: "m", SystemPrompt: "sys", Temperature: &temp, ThinkingLevel: sdk.ThinkingMedium, MaxOutputTokens: 200, Messages: []sdk.Turn{
		{Role: sdk.RoleModel, Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "calling"}}},
		{Role: sdk.RoleToolCall, ToolCall: &sdk.ToolCall{ID: "c1", Name: "bash", Arguments: `{"command":"pwd"}`}},
		{Role: sdk.RoleToolResult, ToolResult: &sdk.ToolResult{ID: "c1", Content: "/tmp"}},
	}, Tools: []sdk.Tool{{Name: "bash", Description: "run", InputSchema: map[string]any{"type": "object"}}}}
	direct, _, err := build(req)
	if err != nil {
		t.Fatal(err)
	}
	viaCanonical, _, err := BuildFromOpenAI(openai.BuildResponsesRequest(req), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(direct["input"].([]any)) != len(viaCanonical["input"].([]any)) {
		t.Fatalf("input diverged: %#v vs %#v", direct["input"], viaCanonical["input"])
	}
}

func TestToInteractionResponseKeepsProvider(t *testing.T) {
	r := interactionResponse{ID: "int_1", Steps: []interactionStep{
		{Type: "model_output", Content: []interactionContent{{Type: "text", Text: "hello"}}},
	}}
	got := ToInteractionResponse(r, "m")
	if got.Provider != "gemini" || len(got.Content) != 1 || got.Content[0].Text != "hello" {
		t.Fatalf("unexpected canonical response: %+v", got)
	}
}
