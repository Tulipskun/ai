package gemini

import (
	"testing"
	"github.com/Tulipskun/ai/sdk"
)

func TestBuildMapsModelToGeminiModelRole(t *testing.T) {
	r:=build(sdk.Request{Model:"test",Messages:[]sdk.Turn{{Role:sdk.RoleModel,Content:[]sdk.ContentPart{{Type:sdk.ContentText,Text:"hello"}}}}})
	contents:=r["contents"].([]any)
	if contents[0].(map[string]any)["role"]!="model"{t.Fatal("model role must remain Gemini model")}
}
