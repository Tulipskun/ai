package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

func TestModelSettingsModalHasFiveTextInputs(t *testing.T) {
	data := modelSettingsModal("discord:channel:123", "google", "gemini-2.5-flash", "0.7", "medium", "2")
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

func TestModelSettingsModalKeepsCurrentValues(t *testing.T) {
	data := modelSettingsModal("discord:channel:123", "openai", "gpt-5", "0.4", "high", "3")
	want := map[string]string{
		"provider": "openai",
		"model": "gpt-5",
		"temperature": "0.4",
		"thinking": "high",
		"key": "3",
	}
	for _, row := range data.Components {
		actionRow := row.(discordgo.ActionsRow)
		input := actionRow.Components[0].(discordgo.TextInput)
		if input.Value != want[input.CustomID] {
			t.Fatalf("%s value = %q, want %q", input.CustomID, input.Value, want[input.CustomID])
		}
	}
}

func TestModelSettingsModalUsesSafeDefaults(t *testing.T) {
	data := modelSettingsModal("discord:channel:123", "", "", "default", "", "0")
	values := make(map[string]string)
	for _, row := range data.Components {
		actionRow := row.(discordgo.ActionsRow)
		input := actionRow.Components[0].(discordgo.TextInput)
		values[input.CustomID] = input.Value
	}
	if values["thinking"] != string(sdk.ThinkingMedium) {
		t.Fatalf("thinking default = %q, want medium", values["thinking"])
	}
	if values["key"] != "1" {
		t.Fatalf("key default = %q, want 1", values["key"])
	}
	if values["temperature"] != "" {
		t.Fatalf("temperature default = %q, want empty", values["temperature"])
	}
}
