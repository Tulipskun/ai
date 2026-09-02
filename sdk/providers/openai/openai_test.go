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

func TestBuildReplaysResponsesReasoning(t *testing.T) {
	r:=build(sdk.Request{Model:"deepseek-v4-flash",ThinkingLevel:sdk.ThinkingHigh,Messages:[]sdk.Turn{
		{Role:sdk.RoleUser,Content:[]sdk.ContentPart{{Type:sdk.ContentText,Text:"use the tool"}}},
		{Role:sdk.RoleModel,Reasoning:&sdk.ReasoningState{ID:"rs_123",Text:"think before using the tool"}},
		{Role:sdk.RoleToolCall,ToolCall:&sdk.ToolCall{ID:"c1",Name:"bash",Arguments:`{"command":"pwd"}`}},
		{Role:sdk.RoleToolResult,ToolResult:&sdk.ToolResult{ID:"c1",Content:"/tmp"}},
	}})
	input:=r["input"].([]any)
	reasoning,ok:=input[1].(map[string]any)
	if !ok||reasoning["type"]!="reasoning"{t.Fatalf("reasoning item missing: %#v",input)}
	if reasoning["id"]!="rs_123"{t.Fatalf("reasoning id=%v",reasoning["id"])}
	content:=reasoning["content"].([]any)
	part:=content[0].(map[string]any)
	if part["type"]!="reasoning_text"||part["text"]!="think before using the tool"{t.Fatalf("reasoning content=%#v",part)}
}

func TestParseResponsePreservesReasoning(t *testing.T) {
	r:=response{Model:"deepseek-v4-flash",Output:[]struct{Type string `json:"type"`;ID string `json:"id"`;CallID string `json:"call_id"`;Name string `json:"name"`;Arguments string `json:"arguments"`;Content []struct{Type string `json:"type"`;Text string `json:"text"`} `json:"content"`}{
		{Type:"reasoning",ID:"rs_123",Content:[]struct{Type string `json:"type"`;Text string `json:"text"`}{{Type:"reasoning_text",Text:"think"}}},
	}}
	got:=parseResponse(r)
	if got.Reasoning==nil||got.Reasoning.ID!="rs_123"||got.Reasoning.Text!="think"{t.Fatalf("reasoning=%#v",got.Reasoning)}
}
