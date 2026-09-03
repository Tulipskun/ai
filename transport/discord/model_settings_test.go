package discord

import (
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func TestFilterModels(t *testing.T) {
	models := []sdk.Model{
		{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro"},
		{ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash"},
		{ID: "qwen3-30b", Name: "Qwen3 30B"},
	}

	got := filterModels(models, "FLASH")
	if len(got) != 1 || got[0].ID != "gemini-2.5-flash" {
		t.Fatalf("filterModels() = %#v, want Gemini 2.5 Flash", got)
	}
}

func TestFilterModelsEmptyQueryReturnsAll(t *testing.T) {
	models := make([]sdk.Model, 30)
	for i := range models {
		models[i] = sdk.Model{ID: "model-" + string(rune('a'+i%26))}
	}

	got := filterModels(models, "")
	if len(got) != len(models) {
		t.Fatalf("filterModels() returned %d models, want %d", len(got), len(models))
	}
}
