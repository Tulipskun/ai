package discord

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

const (
	// modelStep1ModalID is the custom ID of the provider-selection modal
	// opened by /model. The invoking channel travels on the interaction
	// itself, so no channel suffix is needed.
	modelStep1ModalID = "model:step1"
	// modelStep2Prefix prefixes the model-selection modal custom ID, which
	// carries the channel and provider the second step applies to.
	modelStep2Prefix = "model:step2:"
)

const (
	// modelStep2Menus caps the model select menus in the step-2 modal. A
	// modal holds at most five components, and temperature, thinking, and
	// API pool already take three, so two model menus is the maximum that
	// still fits everything in one modal (REQ-027-era modal flow).
	modelStep2Menus = 2
	// modelMenuOptions caps the options of one select menu (Discord limit).
	modelMenuOptions = 25
)

// maxStep2Models is the largest catalogue slice one step-2 modal can offer.
const maxStep2Models = modelStep2Menus * modelMenuOptions

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
		return h.openStep1Modal(s, i)
	}
	if i.Type != discordgo.InteractionModalSubmit { return nil }
	customID := i.ModalSubmitData().CustomID
	if customID == modelStep1ModalID { return h.submitStep1(s, i) }
	if strings.HasPrefix(customID, modelStep2Prefix) { return h.submitStep2(s, i, customID) }
	return nil
}

// modelStep1Modal builds the provider-selection modal opened by /model: one
// provider select plus an optional model filter that narrows the step-2 model
// menus for large catalogues.
func modelStep1Modal(providers []sdk.ProviderID) *discordgo.InteractionResponseData {
	return &discordgo.InteractionResponseData{CustomID: modelStep1ModalID, Title: "Model Settings — Step 1", Components: []discordgo.MessageComponent{
		discordgo.Label{Label: "Provider", Description: "Choose the provider first.", Component: discordgo.SelectMenu{CustomID: "provider", MenuType: discordgo.StringSelectMenu, Placeholder: "Select provider", Options: makeProviderOptions(providers, ""), Required: boolPtr(true)}},
		discordgo.Label{Label: "Model filter", Description: "Type to narrow the model list. Leave blank for all models.", Component: discordgo.TextInput{CustomID: "filter", Style: discordgo.TextInputShort, Placeholder: "e.g. qwen", Required: boolPtr(false), MaxLength: 100}},
	}}
}

func (h *ModelSettingsHandler) openStep1Modal(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if len(h.Providers) == 0 { return h.respondError(s, i, "no providers are configured") }
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseModal, Data: modelStep1Modal(h.Providers)})
}

// filterModels keeps the catalogue entries whose ID contains the filter
// (case-insensitive). A blank filter keeps everything.
func filterModels(models []sdk.Model, filter string) []sdk.Model {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return models
	}
	out := make([]sdk.Model, 0, len(models))
	for _, model := range models {
		if model.ID == "" {
			continue
		}
		if strings.Contains(strings.ToLower(model.ID), filter) {
			out = append(out, model)
		}
	}
	return out
}

// modelStep2Modal builds the model-selection modal: up to two model select
// menus (25 options each), temperature, thinking level, and API key pool.
// The current session config preselects the existing values. It errors when
// no model matches or when more models match than one modal can offer, so the
// caller can ask for a narrower filter instead of silently dropping models.
func modelStep2Modal(channelID string, provider sdk.ProviderID, models []sdk.Model, config sdk.SessionConfig, keyCount int) (*discordgo.InteractionResponseData, error) {
	if len(models) == 0 {
		return nil, fmt.Errorf("no models match; change the filter")
	}
	if len(models) > maxStep2Models {
		return nil, fmt.Errorf("%d models match; narrow the filter to at most %d", len(models), maxStep2Models)
	}
	if keyCount < 1 {
		keyCount = 1
	}
	groups := makeModelOptionGroups(models, config.Model)
	components := make([]discordgo.MessageComponent, 0, len(groups)+3)
	for index, options := range groups {
		label := "Model"
		if len(groups) > 1 {
			label = fmt.Sprintf("Models %d-%d", index*modelMenuOptions+1, index*modelMenuOptions+len(options))
		}
		components = append(components, discordgo.Label{Label: label, Description: "Choose the model.", Component: discordgo.SelectMenu{CustomID: fmt.Sprintf("model_%d", index), MenuType: discordgo.StringSelectMenu, Placeholder: "Select model", Options: options, Required: boolPtr(false)}})
	}
	components = append(components,
		discordgo.Label{Label: "Temperature", Description: "Enter a number from 0.0 to 2.0. Leave blank for default.", Component: discordgo.TextInput{CustomID: "temperature", Style: discordgo.TextInputShort, Value: normalizeTemperatureInput(temperatureLabel(config.Temperature)), Placeholder: "0.0 - 2.0", Required: boolPtr(false), MaxLength: 20}},
		discordgo.Label{Label: "Thinking", Component: discordgo.SelectMenu{CustomID: "thinking", MenuType: discordgo.StringSelectMenu, Placeholder: "Select thinking level", Options: makeThinkingOptions(string(config.ThinkingLevel)), Required: boolPtr(true)}},
		discordgo.Label{Label: "API Pool", Component: discordgo.SelectMenu{CustomID: "key", MenuType: discordgo.StringSelectMenu, Placeholder: "Select API key pool", Options: makeKeyOptions(keyCount, strconv.Itoa(config.KeyIndex+1)), Required: boolPtr(true)}},
	)
	return &discordgo.InteractionResponseData{CustomID: string(modelStep2Prefix + channelID + ":" + string(provider)), Title: "Model Settings — Step 2", Components: components}, nil
}

func (h *ModelSettingsHandler) submitStep1(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	values := modalValues(i)
	provider := sdk.ProviderID(strings.TrimSpace(values["provider"]))
	if provider == "" {
		return h.respondError(s, i, "provider is required")
	}
	if !h.hasProvider(provider) {
		return h.respondError(s, i, fmt.Sprintf("unknown provider %q", provider))
	}
	if h.ProviderKeys[provider] == nil {
		return h.respondError(s, i, fmt.Sprintf("provider %q has no API key pool", provider))
	}
	if h.Models == nil {
		return h.respondError(s, i, "model catalogue loader is not configured")
	}
	models, err := h.Models(context.Background(), provider)
	if err != nil {
		return h.respondError(s, i, err.Error())
	}
	models = filterModels(models, values["filter"])
	if h.ResolveSession == nil {
		return h.respondError(s, i, "session manager is not configured")
	}
	session, err := h.ResolveSession(context.Background(), sdk.Input{SessionID: h.sessionIDFor(i.ChannelID)})
	if err != nil {
		return h.respondError(s, i, err.Error())
	}
	keyCount := 1
	if keys := h.ProviderKeys[provider]; keys != nil && keys.Len() > 0 {
		keyCount = keys.Len()
	}
	data, err := modelStep2Modal(i.ChannelID, provider, models, session.Config(), keyCount)
	if err != nil {
		return h.respondError(s, i, err.Error())
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseModal, Data: data})
}

// applyModelStep2 validates one step-2 modal submission and applies it to the
// session. The model is the first non-empty model menu value, because exactly
// one of the menus carries the user's choice.
func applyModelStep2(session *sdk.Session, keys *sdk.KeyPool, provider sdk.ProviderID, values map[string]string, models []sdk.Model) error {
	if session == nil {
		return fmt.Errorf("session manager is not configured")
	}
	if keys == nil {
		return fmt.Errorf("provider %q has no API key pool", provider)
	}
	model := ""
	for index := 0; index < modelStep2Menus; index++ {
		if candidate := strings.TrimSpace(values[fmt.Sprintf("model_%d", index)]); candidate != "" {
			model = candidate
			break
		}
	}
	if model == "" {
		return fmt.Errorf("model is required")
	}
	if !modelInCatalog(models, model) {
		return fmt.Errorf("model %q is not available for provider %q", model, provider)
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
	keyIndex, parseErr := strconv.Atoi(keyText)
	if parseErr != nil || keyIndex < 1 {
		return fmt.Errorf("API Pool must be a positive number")
	}
	keyIndex--
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

func (h *ModelSettingsHandler) submitStep2(s interactionAPI, i *discordgo.InteractionCreate, customID string) error {
	value := strings.TrimPrefix(customID, modelStep2Prefix)
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return h.respondError(s, i, "invalid model settings session")
	}
	channelID := parts[0]
	provider := sdk.ProviderID(parts[1])
	if !h.hasProvider(provider) {
		return h.respondError(s, i, fmt.Sprintf("unknown provider %q", provider))
	}
	if h.ResolveSession == nil {
		return h.respondError(s, i, "session manager is not configured")
	}
	// Catalogue loads and session writes can outlast the 3-second
	// interaction window, so defer first and report every outcome as a
	// follow-up (REQ-024).
	if err := deferEphemeralResponse(s, i); err != nil {
		return err
	}
	session, err := h.ResolveSession(context.Background(), sdk.Input{SessionID: h.sessionIDFor(channelID)})
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
	if err := applyModelStep2(session, h.ProviderKeys[provider], provider, modalValues(i), models); err != nil {
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
func makeProviderOptions(providers []sdk.ProviderID, current string) []discordgo.SelectMenuOption { options:=make([]discordgo.SelectMenuOption,0,min(len(providers),25));for _,provider:=range providers{value:=string(provider);if value==""||len(options)>=25{continue};options=append(options,discordgo.SelectMenuOption{Label:value,Value:value,Default:false})};return options }
func makeModelOptionGroups(models []sdk.Model, current string) [][]discordgo.SelectMenuOption { all:=make([]discordgo.SelectMenuOption,0,len(models));for _,model:=range models{if model.ID==""{continue};all=append(all,discordgo.SelectMenuOption{Label:model.ID,Value:model.ID,Default:model.ID==current})};groups:=make([][]discordgo.SelectMenuOption,0,(len(all)+24)/25);for start:=0;start<len(all);start+=25{end:=start+25;if end>len(all){end=len(all)};group:=make([]discordgo.SelectMenuOption,end-start);copy(group,all[start:end]);groups=append(groups,group)};return groups }
func makeTemperatureOptions(current string) []discordgo.SelectMenuOption { return []discordgo.SelectMenuOption{{Label:current,Value:current,Default:true}} }
func makeThinkingOptions(current string) []discordgo.SelectMenuOption {values:=[]string{"default",string(sdk.ThinkingNone),string(sdk.ThinkingLow),string(sdk.ThinkingMedium),string(sdk.ThinkingHigh)};options:=make([]discordgo.SelectMenuOption,0,len(values));for _,value:=range values{label:=value;if value=="default"{label="Default"};options=append(options,discordgo.SelectMenuOption{Label:label,Value:value,Default:value==current||(current==""&&value=="default")})};return options }
func makeKeyOptions(count int,current string) []discordgo.SelectMenuOption {if count<1{count=1};if count>25{count=25};currentIndex,_:=strconv.Atoi(current);options:=make([]discordgo.SelectMenuOption,0,count);for index:=1;index<=count;index++{value:=strconv.Itoa(index);options=append(options,discordgo.SelectMenuOption{Label:"Pool "+value,Value:value,Default:index==currentIndex})};if currentIndex<1||currentIndex>count{options[0].Default=true};return options }
func(h *ModelSettingsHandler)respondError(s interactionAPI,i *discordgo.InteractionCreate,message string)error{data:=&discordgo.InteractionResponseData{Content:"Model settings error: "+message,Flags:discordgo.MessageFlagsEphemeral};return s.InteractionRespond(i.Interaction,&discordgo.InteractionResponse{Type:discordgo.InteractionResponseChannelMessageWithSource,Data:data})}
