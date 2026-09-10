package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

func TestProviderSettingsModalContainsRequiredFields(t *testing.T) {
	h := &ProviderSettingsHandler{Adapters: []sdk.AdapterID{sdk.AdapterOpenAI, sdk.AdapterAnthropic, sdk.AdapterGemini}}
	data := h.modalData()
	if len(data.Components) != 4 {
		t.Fatalf("components = %d, want 4", len(data.Components))
	}
	want := []string{"Name", "Adapter", "URL", "API Key"}
	for i, label := range data.Components {
		component, ok := label.(discordgo.Label)
		if !ok { t.Fatalf("component %d = %T, want discordgo.Label", i, label) }
		if component.Label != want[i] { t.Fatalf("component %d label = %q, want %q", i, component.Label, want[i]) }
	}
	adapter, ok := data.Components[1].(discordgo.Label).Component.(discordgo.SelectMenu)
	if !ok { t.Fatalf("adapter component = %T, want discordgo.SelectMenu", data.Components[1].(discordgo.Label).Component) }
	if len(adapter.Options) != 3 { t.Fatalf("adapter options = %d, want 3", len(adapter.Options)) }
}

func TestProviderSettingsModalID(t *testing.T) {
	h := &ProviderSettingsHandler{Adapters: []sdk.AdapterID{sdk.AdapterOpenAI}}
	if h.modalData().CustomID != providerSettingsModalID { t.Fatalf("custom ID = %q, want %q", h.modalData().CustomID, providerSettingsModalID) }
}
