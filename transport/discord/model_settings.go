package discord

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

// modelSetupModalID is the custom ID of the single /model modal. The invoking
// channel travels on the interaction itself, so no channel suffix is needed.
//
// The flow is a single modal on purpose: Discord does not allow opening a
// modal in response to a modal submit, so the provider picker, the model
// field, temperature, thinking level, and API pool all live in one modal
// (CHANGE-010). Five labels is exactly the modal component limit.
const modelSetupModalID = "model:setup"

type ModelSettingsHandler struct {
	ResolveSession   func(context.Context, sdk.Input) (*sdk.Session, error)
	SessionForChannel func(channelID string) string
	Providers        []sdk.ProviderID
	ProviderKeys     map[sdk.ProviderID]*sdk.KeyPool
	Models           func(context.Context, sdk.ProviderID) ([]sdk.Model, error)
}

func (h *ModelSettingsHandler) sessionIDFor(channelID string) string {
	channelID = strings.TrimSpace(channelID)
	if h != nil && h.SessionForChannel != nil {
		if id := strings.TrimSpace(h.SessionForChannel(channelID)); id != "" {
			return id
		}
	}
	return "discord:channel:" + channelID
}

func (h *ModelSettingsHandler) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if h == nil || i == nil { return nil }
	if i.Type == discordgo.InteractionApplicationCommand {
		if i.ApplicationCommandData().Name != "model" { return nil }
		return h.openSetupModal(s, i)
	}
	if i.Type == discordgo.InteractionModalSubmit && i.ModalSubmitData().CustomID == modelSetupModalID {
		return h.submitSetup(s, i)
	}
	return nil
}

// modelSetupModal builds the single /model modal, preselecting the session's
// current values so a user who only tweaks temperature keeps everything else.
func modelSetupModal(providers []sdk.ProviderID, config sdk.SessionConfig) *discordgo.InteractionResponseData {
	return &discordgo.InteractionResponseData{CustomID: modelSetupModalID, Title: "Model Settings", Components: []discordgo.MessageComponent{
		discordgo.Label{Label: "Provider", Description: "Choose the provider.", Component: discordgo.SelectMenu{CustomID: "provider", MenuType: discordgo.StringSelectMenu, Placeholder: "Select provider", Options: makeProviderOptions(providers, string(config.Provider)), Required: boolPtr(true)}},
		discordgo.Label{Label: "Model", Description: "Model ID, or a unique part of it.", Component: discordgo.TextInput{CustomID: "model", Style: discordgo.TextInputShort, Value: config.Model, Placeholder: "e.g. qwen3.8-flash", Required: boolPtr(true), MaxLength: 100}},
		discordgo.Label{Label: "Temperature", Description: "Enter a number from 0.0 to 2.0. Leave blank for default.", Component: discordgo.TextInput{CustomID: "temperature", Style: discordgo.TextInputShort, Value: normalizeTemperatureInput(temperatureLabel(config.Temperature)), Placeholder: "0.0 - 2.0", Required: boolPtr(false), MaxLength: 20}},
		discordgo.Label{Label: "Thinking", Component: discordgo.SelectMenu{CustomID: "thinking", MenuType: discordgo.StringSelectMenu, Placeholder: "Select thinking level", Options: makeThinkingOptions(string(config.ThinkingLevel)), Required: boolPtr(true)}},
		discordgo.Label{Label: "API Pool", Description: "Key pool number. Leave blank to keep the current one.", Component: discordgo.TextInput{CustomID: "key", Style: discordgo.TextInputShort, Value: strconv.Itoa(config.KeyIndex + 1), Placeholder: "1", Required: boolPtr(false), MaxLength: 5}},
	}}
}

func (h *ModelSettingsHandler) openSetupModal(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if len(h.Providers) == 0 {
		return h.respondError(s, i, "no providers are configured")
	}
	if h.ResolveSession == nil {
		return h.respondError(s, i, "session manager is not configured")
	}
	// A session resolve is a fast local read; the modal must open inside the
	// 3-second window, so nothing slower may precede the response.
	session, err := h.ResolveSession(context.Background(), sdk.Input{SessionID: h.sessionIDFor(i.ChannelID)})
	if err != nil {
		return h.respondError(s, i, err.Error())
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseModal, Data: modelSetupModal(h.Providers, session.Config())})
}

// resolveModelID maps the typed model field to a catalogue entry: an exact
// ID wins, otherwise a case-insensitive unique substring wins. Anything else
// is an error that tells the user how to fix the input.
func resolveModelID(models []sdk.Model, provider sdk.ProviderID, input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("model is required")
	}
	if modelInCatalog(models, input) {
		return input, nil
	}
	lower := strings.ToLower(input)
	matches := make([]string, 0, 4)
	for _, model := range models {
		if model.ID == "" || len(matches) > 10 {
			continue
		}
		if strings.Contains(strings.ToLower(model.ID), lower) {
			matches = append(matches, model.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("model %q is not available for provider %q", input, provider)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("model %q is ambiguous; be more specific (matches: %s)", input, strings.Join(matches, ", "))
	}
}

// applyModelSetup validates one setup modal submission and applies it to the
// session. A blank key field keeps the session's current pool index.
func applyModelSetup(session *sdk.Session, keys *sdk.KeyPool, provider sdk.ProviderID, values map[string]string, models []sdk.Model) error {
	if session == nil {
		return fmt.Errorf("session manager is not configured")
	}
	if keys == nil {
		return fmt.Errorf("provider %q has no API key pool", provider)
	}
	model, err := resolveModelID(models, provider, values["model"])
	if err != nil {
		return err
	}
	thinking := strings.TrimSpace(strings.ToLower(values["thinking"]))
	temperatureText := strings.TrimSpace(values["temperature"])
	keyText := strings.TrimSpace(values["key"])
	var temperature *float64
	if temperatureText != "" && temperatureText != "default" {
		value, parseErr := strconv.ParseFloat(temperatureText, 64)
		if parseErr != nil || value < 0 || value > 2 {
			return fmt.Errorf("temperature must be a number from 0.0 to 2.0")
		}
		temperature = &value
	}
	if thinking != "" && thinking != "default" && !validDiscordThinkingLevel(thinking) {
		return fmt.Errorf("invalid thinking level %q", thinking)
	}
	keyIndex := session.Config().KeyIndex
	if keyText != "" {
		parsed, parseErr := strconv.Atoi(keyText)
		if parseErr != nil || parsed < 1 {
			return fmt.Errorf("API Pool must be a positive number")
		}
		keyIndex = parsed - 1
	}
	if _, err := keys.At(keyIndex); err != nil {
		return err
	}
	if err := session.SetProvider(provider, keys); err != nil {
		return err
	}
	if err := session.SetModel(model); err != nil {
		return err
	}
	if temperature == nil {
		if err := session.ClearTemperature(); err != nil {
			return err
		}
	} else if err := session.SetTemperature(*temperature); err != nil {
		return err
	}
	if thinking == "" || thinking == "default" {
		if err := session.ClearThinkingLevel(); err != nil {
			return err
		}
	} else if err := session.SetThinkingLevel(sdk.ThinkingLevel(thinking)); err != nil {
		return err
	}
	if err := session.SetKeyIndex(keyIndex); err != nil {
		return err
	}
	return nil
}

func (h *ModelSettingsHandler) submitSetup(s interactionAPI, i *discordgo.InteractionCreate) error {
	provider := sdk.ProviderID(strings.TrimSpace(modalValues(i)["provider"]))
	if provider == "" {
		return h.respondError(s, i, "provider is required")
	}
	if !h.hasProvider(provider) {
		return h.respondError(s, i, fmt.Sprintf("unknown provider %q", provider))
	}
	// Catalogue loads and session writes can outlast the 3-second
	// interaction window, so defer first and report every outcome as a
	// follow-up (REQ-024).
	if err := deferEphemeralResponse(s, i); err != nil {
		return err
	}
	if h.ResolveSession == nil {
		return followupEphemeral(s, i, "Model settings error: session manager is not configured")
	}
	session, err := h.ResolveSession(context.Background(), sdk.Input{SessionID: h.sessionIDFor(i.ChannelID)})
	if err != nil {
		return followupEphemeral(s, i, "Model settings error: "+err.Error())
	}
	var models []sdk.Model
	if h.Models != nil {
		models, err = h.Models(context.Background(), provider)
		if err != nil {
			return followupEphemeral(s, i, "Model settings error: "+err.Error())
		}
	}
	if err := applyModelSetup(session, h.ProviderKeys[provider], provider, modalValues(i), models); err != nil {
		return followupEphemeral(s, i, "Model settings error: "+err.Error())
	}
	return followupEphemeral(s, i, sessionSettingsSummary(session.Config()))
}

func modalValues(i *discordgo.InteractionCreate) map[string]string { values:=make(map[string]string,6);for _,component:=range i.ModalSubmitData().Components{label,ok:=component.(*discordgo.Label);if !ok{if valueLabel,valueOK:=component.(discordgo.Label);valueOK{label=&valueLabel}else{continue}};switch child:=label.Component.(type){case *discordgo.SelectMenu:if len(child.Values)>0{values[child.CustomID]=child.Values[0]};case discordgo.SelectMenu:if len(child.Values)>0{values[child.CustomID]=child.Values[0]};case *discordgo.TextInput:values[child.CustomID]=child.Value;case discordgo.TextInput:values[child.CustomID]=child.Value}};return values }
func modalSelectValues(i *discordgo.InteractionCreate) map[string]string{return modalValues(i)}
func normalizeTemperatureInput(value string) string {value=strings.TrimSpace(value);if value=="default"{return ""};return value}
func validDiscordThinkingLevel(level string) bool {switch sdk.ThinkingLevel(level){case sdk.ThinkingNone,sdk.ThinkingLow,sdk.ThinkingMedium,sdk.ThinkingHigh:return true;default:return false}}
func modelInCatalog(models []sdk.Model, model string) bool {for _,candidate:=range models{if candidate.ID==model{return true}};return false}
func(h *ModelSettingsHandler)hasProvider(provider sdk.ProviderID)bool{for _,candidate:=range h.Providers{if candidate==provider{return true}};return false}
func(h *ModelSettingsHandler)summary(session *sdk.Session)string{return sessionSettingsSummary(session.Config())}

// sessionSettingsSummary renders the settings report shared by the /model flow
// and the /new channel summary, so both surfaces always describe the same
// fields (REQ-027).
func sessionSettingsSummary(config sdk.SessionConfig)string{thinking:=string(config.ThinkingLevel);if thinking==""{thinking="default"};model:=config.Model;if model==""{model="not set"};return fmt.Sprintf("**Session Model Settings**\nProvider: `%s`\nModel: `%s`\nThinking: `%s`\nTemperature: `%s`\nAPI Pool: `%d`",config.Provider,model,thinking,temperatureLabel(config.Temperature),config.KeyIndex+1)}
func temperatureLabel(value *float64)string{if value==nil{return "default"};return strconv.FormatFloat(*value,'f',1,64)}
func boolPtr(value bool)*bool{return &value}
func makeProviderOptions(providers []sdk.ProviderID, current string) []discordgo.SelectMenuOption { options:=make([]discordgo.SelectMenuOption,0,min(len(providers),25));for _,provider:=range providers{value:=string(provider);if value==""||len(options)>=25{continue};options=append(options,discordgo.SelectMenuOption{Label:value,Value:value,Default:value==current})};return options }
func makeTemperatureOptions(current string) []discordgo.SelectMenuOption { return []discordgo.SelectMenuOption{{Label:current,Value:current,Default:true}} }
func makeThinkingOptions(current string) []discordgo.SelectMenuOption {values:=[]string{"default",string(sdk.ThinkingNone),string(sdk.ThinkingLow),string(sdk.ThinkingMedium),string(sdk.ThinkingHigh)};options:=make([]discordgo.SelectMenuOption,0,len(values));for _,value:=range values{label:=value;if value=="default"{label="Default"};options=append(options,discordgo.SelectMenuOption{Label:label,Value:value,Default:value==current||(current==""&&value=="default")})};return options }
func(h *ModelSettingsHandler)respondError(s interactionAPI,i *discordgo.InteractionCreate,message string)error{data:=&discordgo.InteractionResponseData{Content:"Model settings error: "+message,Flags:discordgo.MessageFlagsEphemeral};return s.InteractionRespond(i.Interaction,&discordgo.InteractionResponse{Type:discordgo.InteractionResponseChannelMessageWithSource,Data:data})}
