package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
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

func TestModelSettingsModalHasFiveTextInputs(t *testing.T) {
	data := modelSettingsModal("discord:channel:123")
	if data.Type != discordgo.InteractionResponseModal {
		t.Fatalf("response type = %v, want modal", data.Type)
	}
	if data.Title != "Model Settings" {
		t.Fatalf("title = %q, want Model Settings", data.Title)
	}
	if data.CustomID != "model:settings:123" {
		t.Fatalf("custom ID = %q, want model:settings:123", data.CustomID)
	}
	if len(data.Components) != 5 {
		t.Fatalf("modal rows = %d, want 5", len(data.Components))
	}

	want := []string{"provider", "model", "temperature", "thinking", "key"}
	for index, row := range data.Components {
		actionRow, ok := row.(discordgo.ActionsRow)
		if !ok || len(actionRow.Components) != 1 {
			t.Fatalf("row %d is not a single-component action row", index)
		}
		input, ok := actionRow.Components[0].(discordgo.TextInput)
		if !ok {
			t.Fatalf("row %d component is %T, want TextInput", index, actionRow.Components[0])
		}
		if input.CustomID != want[index] {
			t.Fatalf("row %d custom ID = %q, want %q", index, input.CustomID, want[index])
		}
	}
}
