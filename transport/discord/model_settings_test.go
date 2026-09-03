package discord

import (
	"encoding/json"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

func TestProviderSelectionModalUsesSingleStringSelect(t *testing.T) {
	data := providerSelectionModal("123", "B.ai", []sdk.ProviderID{"B.ai", "google"})
	if data.Title != "Model Settings — Step 1" {
		t.Fatalf("title = %q, want Step 1", data.Title)
	}
	if len(data.Components) != 1 {
		t.Fatalf("components = %d, want 1", len(data.Components))
	}
	label := data.Components[0].(discordgo.Label)
	selectMenu := label.Component.(discordgo.SelectMenu)
	if selectMenu.CustomID != "provider" || len(selectMenu.Options) != 2 {
		t.Fatalf("unexpected provider select: %+v", selectMenu)
	}
	if !selectMenu.Options[0].Default {
		t.Fatal("current provider is not selected by default")
	}
}

func TestModelSettingsModalUsesModernComponents(t *testing.T) {
	data := modelSettingsModal("discord:channel:123", "google", "gemini-2.5-flash", "0.7", "medium", "2")
	if data.Title != "Model Settings — Step 2" {
		t.Fatalf("title = %q, want Step 2", data.Title)
	}
	if data.CustomID != "model:settings:123:google" {
		t.Fatalf("custom ID = %q, want provider-qualified ID", data.CustomID)
	}
	if len(data.Components) != 4 {
		t.Fatalf("components = %d, want 4", len(data.Components))
	}

	payload, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal modal: %v", err)
	}
	var decoded struct {
		Components []struct {
			Type      int `json:"type"`
			Component struct {
				Type int `json:"type"`
			} `json:"component"`
		} `json:"components"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode modal: %v", err)
	}
	for index, component := range decoded.Components {
		if component.Type != 18 {
			t.Fatalf("component %d type = %d, want Label (18)", index, component.Type)
		}
		if component.Component.Type != 3 {
			t.Fatalf("component %d child type = %d, want String Select (3)", index, component.Component.Type)
		}
	}
}

func TestModelSettingsModalKeepsCurrentValuesSelected(t *testing.T) {
	data := modelSettingsModal("discord:channel:123", "openai", "gpt-5", "0.4", "high", "3")
	for _, component := range data.Components {
		label, ok := component.(discordgo.Label)
		if !ok {
			t.Fatalf("component is %T, want discordgo.Label", component)
		}
		selectMenu, ok := label.Component.(discordgo.SelectMenu)
		if !ok {
			t.Fatalf("label child is %T, want discordgo.SelectMenu", label.Component)
		}
		found := false
		for _, option := range selectMenu.Options {
			if option.Default {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s has no default option", label.Label)
		}
	}
}

func TestModelSettingsModalUsesCatalogModels(t *testing.T) {
	models := []sdk.Model{{ID: "qwen3.8-flash"}, {ID: "qwen3.5-plus"}}
	data := modelSettingsModalWithCatalog("discord:channel:123", "B.ai", "qwen3.8-flash", "default", "none", "1", models, 2)
	if len(data.Components) != 4 {
		t.Fatalf("components = %d, want 4", len(data.Components))
	}
	modelSelect := data.Components[0].(discordgo.Label).Component.(discordgo.SelectMenu)
	if len(modelSelect.Options) != 2 {
		t.Fatalf("model options = %d, want 2", len(modelSelect.Options))
	}
	if modelSelect.Options[0].Value != "qwen3.8-flash" || !modelSelect.Options[0].Default {
		t.Fatalf("unexpected first model option: %+v", modelSelect.Options[0])
	}
}
