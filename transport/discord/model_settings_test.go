package discord

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

func TestProviderSelectionComponentDoesNotPreselectCurrentProvider(t *testing.T) {
	data := providerSelectionMessage("123", "B.ai", []sdk.ProviderID{"B.ai", "google"})
	if len(data.Components) != 1 { t.Fatalf("components = %d, want 1", len(data.Components)) }
	row, ok := data.Components[0].(discordgo.ActionsRow); if !ok { t.Fatalf("component = %T, want ActionsRow", data.Components[0]) }
	selectMenu, ok := row.Components[0].(discordgo.SelectMenu); if !ok { t.Fatalf("child = %T, want SelectMenu", row.Components[0]) }
	if selectMenu.CustomID != modelSettingsProviderSelect || len(selectMenu.Options) != 2 { t.Fatalf("unexpected provider select: %+v", selectMenu) }
	for _, option := range selectMenu.Options { if option.Default { t.Fatalf("provider %q is preselected; provider must remain selectable", option.Value) } }
}

func TestModelOptionGroupsIncludeEveryModel(t *testing.T) {
	models := make([]sdk.Model, 0, 76); for index := 1; index <= 76; index++ { models = append(models, sdk.Model{ID: "model-" + strconv.Itoa(index)}) }
	groups := makeModelOptionGroups(models, "model-76")
	if len(groups) != 4 { t.Fatalf("groups = %d, want 4", len(groups)) }
	wantSizes := []int{25, 25, 25, 1}; for index, want := range wantSizes { if len(groups[index]) != want { t.Fatalf("group %d size = %d, want %d", index, len(groups[index]), want) } }
	if groups[3][0].Value != "model-76" || !groups[3][0].Default { t.Fatalf("last model was not preserved: %+v", groups[3][0]) }
}

func TestModelSelectionComponentsUseUniquePagedMenus(t *testing.T) {
	groups := [][]discordgo.SelectMenuOption{{{Label: "m1", Value: "m1"}}, {{Label: "m26", Value: "m26"}}, {{Label: "m51", Value: "m51"}}}
	components := makeModelSelectionComponents("123", "google", groups, 0); if len(components) != 3 { t.Fatalf("components = %d, want 3", len(components)) }
	seen := map[string]bool{}; for _, component := range components { row := component.(discordgo.ActionsRow); menu := row.Components[0].(discordgo.SelectMenu); if seen[menu.CustomID] { t.Fatalf("duplicate custom ID %q", menu.CustomID) }; seen[menu.CustomID] = true }
}

func TestModelSettingsModalUsesTemperatureTextInput(t *testing.T) {
	data := modelSettingsModal("discord:channel:123", "google", "gemini-2.5-flash", "0.7", "medium", "2")
	if len(data.Components) != 4 { t.Fatalf("components = %d, want 4", len(data.Components)) }
	payload, err := json.Marshal(data); if err != nil { t.Fatalf("marshal modal: %v", err) }
	var decoded struct { Components []struct { Type int `json:"type"`; Component struct { Type int `json:"type"`; CustomID string `json:"custom_id"`; Value string `json:"value"` } `json:"component"` } `json:"components"` }
	if err := json.Unmarshal(payload, &decoded); err != nil { t.Fatalf("decode modal: %v", err) }
	if decoded.Components[1].Component.Type != int(discordgo.TextInputComponent) { t.Fatalf("temperature component type = %d, want %d", decoded.Components[1].Component.Type, discordgo.TextInputComponent) }
	if decoded.Components[1].Component.CustomID != "temperature" { t.Fatalf("temperature custom ID = %q, want temperature", decoded.Components[1].Component.CustomID) }
	if decoded.Components[1].Component.Value != "0.7" { t.Fatalf("temperature value = %q, want 0.7", decoded.Components[1].Component.Value) }
}

func TestModelSettingsTemperatureRangeMatchesCLI(t *testing.T) {
	data := modelSettingsModal("discord:channel:123", "openai", "gpt-5", "2.0", "high", "1")
	label := data.Components[1].(discordgo.Label)
	temperature := label.Component.(discordgo.TextInput)
	if label.Description != "Enter a number from 0.0 to 2.0. Leave blank for default." { t.Fatalf("description = %q", label.Description) }
	if temperature.Placeholder != "0.0 - 2.0" { t.Fatalf("placeholder = %q", temperature.Placeholder) }
}

func TestModelSettingsModalKeepsCurrentValuesSelected(t *testing.T) {
	data := modelSettingsModal("discord:channel:123", "openai", "gpt-5", "0.4", "high", "3")
	for _, component := range data.Components {
		label, ok := component.(discordgo.Label); if !ok { t.Fatalf("component is %T, want discordgo.Label", component) }
		if label.Label == "Model" { input, ok := label.Component.(discordgo.TextInput); if !ok || input.Value != "gpt-5" { t.Fatalf("model child/value = %T/%q, want TextInput/gpt-5", label.Component, input.Value) }; continue }
		if label.Label == "Temperature" { input, ok := label.Component.(discordgo.TextInput); if !ok { t.Fatalf("temperature child is %T, want discordgo.TextInput", label.Component) }; if input.Value != "0.4" { t.Fatalf("temperature value = %q, want 0.4", input.Value) }; continue }
		selectMenu, ok := label.Component.(discordgo.SelectMenu); if !ok { t.Fatalf("label child is %T, want discordgo.SelectMenu", label.Component) }
		found := false; for _, option := range selectMenu.Options { if option.Default { found = true; break } }; if !found { t.Fatalf("%s has no default option", label.Label) }
	}
}

func TestModelSettingsModalUsesCatalogModels(t *testing.T) {
	models := []sdk.Model{{ID: "qwen3.8-flash"}, {ID: "qwen3.5-plus"}}
	data := modelSettingsModalWithCatalog("discord:channel:123", "B.ai", "qwen3.8-flash", "default", "none", "1", models, 2)
	if len(data.Components) != 4 { t.Fatalf("components = %d, want 4", len(data.Components)) }
	modelInput := data.Components[0].(discordgo.Label).Component.(discordgo.TextInput)
	if modelInput.Value != "qwen3.8-flash" { t.Fatalf("model value = %q, want qwen3.8-flash", modelInput.Value) }
}

func TestModelInCatalog(t *testing.T) {
	models := []sdk.Model{{ID: "m1"}, {ID: "m2"}}
	if !modelInCatalog(models, "m1") { t.Fatal("expected m1 in catalog") }
	if modelInCatalog(models, "m3") { t.Fatal("did not expect m3 in catalog") }
}
