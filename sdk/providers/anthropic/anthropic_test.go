package anthropic

import (
	"testing"
	"github.com/Tulipskun/ai/sdk"
)

func TestBuildUsesAnthropicAssistantRole(t *testing.T) {
	r:=build(sdk.Request{Model:"test",MaxOutputTokens:1000,Messages:[]sdk.Turn{{Role:sdk.RoleModel,Content:[]sdk.ContentPart{{Type:sdk.ContentText,Text:"hello"}}}}})
	msgs:=r["messages"].([]any)
	if msgs[0].(map[string]any)["role"]!="assistant"{t.Fatal("model role must map to anthropic assistant")}
}
