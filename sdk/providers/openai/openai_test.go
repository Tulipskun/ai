package openai

import (
	"testing"
	"github.com/Tulipskun/ai/sdk"
)

func TestBuildTranslatesToolHistory(t *testing.T) {
	r:=build(sdk.Request{Model:"test",Messages:[]sdk.Turn{
		{Role:sdk.RoleUser,Content:[]sdk.ContentPart{{Type:sdk.ContentText,Text:"hi"}}},
		{Role:sdk.RoleToolCall,ToolCall:&sdk.ToolCall{ID:"c1",Name:"bash",Arguments:`{"command":"pwd"}`}},
		{Role:sdk.RoleToolResult,ToolResult:&sdk.ToolResult{ID:"c1",Content:"/tmp"}},
	}})
	input:=r["input"].([]any)
	if input[1].(map[string]any)["type"]!="function_call"{t.Fatal("tool call was not translated")}
	if input[2].(map[string]any)["type"]!="function_call_output"{t.Fatal("tool result was not translated")}
}
