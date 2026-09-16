package discord

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

func TestProviderSettingsModalContainsRequiredFields(t *testing.T) {
	h := &ProviderSettingsHandler{Adapters: []sdk.AdapterID{sdk.AdapterOpenAI, sdk.AdapterAnthropic, sdk.AdapterGemini, sdk.AdapterOpenCode}}
	data := h.modalData()
	if len(data.Components) != 5 {
		t.Fatalf("components = %d, want 5", len(data.Components))
	}
	want := []string{"Name", "Adapter", "URL", "API Key", "Free only"}
	for i, label := range data.Components {
		component, ok := label.(discordgo.Label)
		if !ok { t.Fatalf("component %d = %T, want discordgo.Label", i, label) }
		if component.Label != want[i] { t.Fatalf("component %d label = %q, want %q", i, component.Label, want[i]) }
	}
	adapter, ok := data.Components[1].(discordgo.Label).Component.(discordgo.SelectMenu)
	if !ok { t.Fatalf("adapter component = %T, want discordgo.SelectMenu", data.Components[1].(discordgo.Label).Component) }
	if len(adapter.Options) != 4 { t.Fatalf("adapter options = %d, want 4", len(adapter.Options)) }
}

func TestProviderSettingsModalID(t *testing.T) {
	h := &ProviderSettingsHandler{Adapters: []sdk.AdapterID{sdk.AdapterOpenAI}}
	if h.modalData().CustomID != providerSettingsModalID { t.Fatalf("custom ID = %q, want %q", h.modalData().CustomID, providerSettingsModalID) }
}

func TestTruncateProviderErrorKeepsShortMessages(t *testing.T) {
	if got := truncateProviderError("short"); got != "short" {
		t.Fatalf("truncate = %q", got)
	}
}

func TestTruncateProviderErrorFitsDiscordLimit(t *testing.T) {
	long := "Provider settings error: " + strings.Repeat("x", 5000)
	got := truncateProviderError(long)
	if len([]rune(got)) > 2000 {
		t.Fatalf("truncated length = %d runes, exceeds Discord limit", len([]rune(got)))
	}
	if !strings.HasPrefix(got, "Provider settings error: ") || !strings.HasSuffix(got, "…") {
		t.Fatal("truncation must keep the prefix and mark omission")
	}
}
