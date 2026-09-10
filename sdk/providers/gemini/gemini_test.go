package gemini

import (
	"testing"
	"github.com/Tulipskun/ai/sdk"
)

func TestBuildMapsModelAndToolResult(t *testing.T) {
	r:=build(sdk.Request{Model:"test",Messages:[]sdk.Turn{
		{Role:sdk.RoleModel,Content:[]sdk.ContentPart{{Type:sdk.ContentText,Text:"calling"}}},
		{Role:sdk.RoleToolCall,ToolCall:&sdk.ToolCall{ID:"c1",Name:"bash",Arguments:`{"command":"pwd"}`}},
		{Role:sdk.RoleToolResult,ToolResult:&sdk.ToolResult{ID:"c1",Content:"/tmp"}},
	}})
	contents:=r["contents"].([]any)
	if contents[0].(map[string]any)["role"]!="model"{t.Fatal("model role must remain Gemini model")}
	part:=contents[2].(map[string]any)["parts"].([]any)[0].(map[string]any)
	fr:=part["functionResponse"].(map[string]any)
	if fr["name"]!="bash"{t.Fatalf("tool result name = %v, want bash",fr["name"])}
}
