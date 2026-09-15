package discord

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

// Custom IDs for the /model message wizard. Every step rewrites the same
// regular channel message (ephemeral messages cannot be edited), so IDs stay
// short: multi-step state lives in the process-local pending store keyed by
// channel and user, not in custom IDs (CHANGE-011).
const (
	modelWizardProvider = "model:provider"
	modelWizardModel    = "model:model"
	modelWizardPagePrev = "model:page:prev"
	modelWizardPageNext = "model:page:next"
	modelWizardTemp     = "model:temp"
	modelWizardThinking = "model:thinking"
	modelWizardKey      = "model:key"
	modelWizardBack     = "model:back"
)

const (
	// modelWizardMenus caps the model select menus on one wizard page. Five
	// action rows fit in a message; the pager row takes one when present.
	modelWizardMenus = 5
	// modelMenuOptions caps the options of one select menu (Discord limit).
	modelMenuOptions = 25
)

// modelPageSize is the largest catalogue slice one wizard page can offer.
const modelPageSize = modelWizardMenus * modelMenuOptions

// modelTemperatures are the temperature presets offered by the wizard. A
// select menu cannot take free text, so the full 0.0-2.0 range is covered by
// representative stops plus the default.
var modelTemperatures = []string{"default", "0.0", "0.3", "0.5", "0.7", "1.0", "1.5", "2.0"}

type modelWizardStage int

const (
	modelStageProvider modelWizardStage = iota
	modelStageModel
	modelStageTemp
	modelStageThinking
	modelStageKey
)

// modelWizard accumulates one invoker's choices until the final step applies
// them all at once. Process-local storage matches the orchestration
// reservation pattern (REQ-021): a restart simply asks the user to rerun
// /model.
type modelWizard struct {
	stage       modelWizardStage
	provider    sdk.ProviderID
	model       string
	temperature string
	thinking    string
	key         string
	page        int
}

type ModelSettingsHandler struct {
	ResolveSession    func(context.Context, sdk.Input) (*sdk.Session, error)
	SessionForChannel func(channelID string) string
	Providers         []sdk.ProviderID
	ProviderKeys      map[sdk.ProviderID]*sdk.KeyPool
	Models            func(context.Context, sdk.ProviderID) ([]sdk.Model, error)

	mu      sync.Mutex
	pending map[string]*modelWizard
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

// wizardKey scopes pending state to the invoking user in the invoking
// channel, so two users configuring side by side never share a wizard.
func wizardKey(channelID, userID string) string {
	return strings.TrimSpace(channelID) + "\x00" + strings.TrimSpace(userID)
}

func interactionUserID(i *discordgo.InteractionCreate) string {
	if i == nil {
		return ""
	}
	if i.Member != nil && i.Member.User != nil && strings.TrimSpace(i.Member.User.ID) != "" {
		return i.Member.User.ID
	}
	if i.User != nil {
		return strings.TrimSpace(i.User.ID)
	}
	return ""
}

func (h *ModelSettingsHandler) startWizard(channelID, userID string) *modelWizard {
	wizard := &modelWizard{stage: modelStageProvider, temperature: "default", thinking: "default", key: "1"}
	h.mu.Lock()
	if h.pending == nil {
		h.pending = make(map[string]*modelWizard)
	}
	h.pending[wizardKey(channelID, userID)] = wizard
	h.mu.Unlock()
	return wizard
}

func (h *ModelSettingsHandler) wizardFor(channelID, userID string) (*modelWizard, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	wizard, ok := h.pending[wizardKey(channelID, userID)]
	return wizard, ok
}

func (h *ModelSettingsHandler) dropWizard(channelID, userID string) {
	h.mu.Lock()
	delete(h.pending, wizardKey(channelID, userID))
	h.mu.Unlock()
}

func (h *ModelSettingsHandler) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if h == nil || i == nil {
		return nil
	}
	if i.Type == discordgo.InteractionApplicationCommand {
		if i.ApplicationCommandData().Name != "model" {
			return nil
		}
		return h.openWizard(s, i)
	}
	if i.Type != discordgo.InteractionMessageComponent {
		return nil
	}
	switch i.MessageComponentData().CustomID {
	case modelWizardProvider, modelWizardModel, modelWizardPagePrev, modelWizardPageNext,
		modelWizardTemp, modelWizardThinking, modelWizardKey, modelWizardBack:
		return h.stepWizard(s, i)
	default:
		return nil
	}
}

func (h *ModelSettingsHandler) openWizard(s interactionAPI, i *discordgo.InteractionCreate) error {
	if len(h.Providers) == 0 {
		return h.respondError(s, i, "no providers are configured")
	}
	h.startWizard(i.ChannelID, interactionUserID(i))
	content, components := providerMenuMessage(h.Providers)
	// A regular channel message (not ephemeral): ephemeral messages cannot
	// be edited, and the whole wizard works by editing this message in place.
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content, Components: components},
	})
}

// providerMenuMessage renders the first wizard step. Provider selection needs
// no session or catalogue reads, so the opening response stays immediate.
func providerMenuMessage(providers []sdk.ProviderID) (string, []discordgo.MessageComponent) {
	minValues := 1
	return "Select a provider:", []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.SelectMenu{CustomID: modelWizardProvider, MenuType: discordgo.StringSelectMenu, Placeholder: "Select provider", Options: makeProviderOptions(providers, ""), MinValues: &minValues, MaxValues: 1},
	}}}
}

// modelPageMessage renders one catalogue page: up to five model menus plus a
// pager row when the catalogue spans several pages. The current session model
// is preselected.
func modelPageMessage(provider sdk.ProviderID, models []sdk.Model, page int, current string) (string, []discordgo.MessageComponent) {
	pages := modelPages(len(models))
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	start := page * modelPageSize
	end := start + modelPageSize
	if end > len(models) {
		end = len(models)
	}
	groups := makeModelOptionGroups(models[start:end], current)
	components := make([]discordgo.MessageComponent, 0, len(groups)+1)
	for index, options := range groups {
		label := "Model"
		if len(groups) > 1 {
			label = fmt.Sprintf("Models %d-%d", start+index*modelMenuOptions+1, start+index*modelMenuOptions+len(options))
		}
		components = append(components, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{CustomID: modelWizardModel, MenuType: discordgo.StringSelectMenu, Placeholder: label, Options: options, MinValues: intPtr(1), MaxValues: 1},
		}})
	}
	content := fmt.Sprintf("Select a model for `%s`:", provider)
	if pages > 1 {
		content = fmt.Sprintf("Select a model for `%s` (page %d of %d):", provider, page+1, pages)
		components = append(components, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{CustomID: modelWizardPagePrev, Style: discordgo.SecondaryButton, Label: "◀ Prev"},
			discordgo.Button{CustomID: modelWizardPageNext, Style: discordgo.SecondaryButton, Label: "Next ▶"},
			discordgo.Button{CustomID: modelWizardBack, Style: discordgo.SecondaryButton, Label: "‹ Providers"},
		}})
	} else {
		components = append(components, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{CustomID: modelWizardBack, Style: discordgo.SecondaryButton, Label: "‹ Providers"},
		}})
	}
	return content, components
}

// modelPages counts the wizard pages for a catalogue of size n.
func modelPages(n int) int {
	if n <= 0 {
		return 1
	}
	return (n + modelPageSize - 1) / modelPageSize
}

// selectMenuMessage renders a single-question wizard step with a Back button.
func selectMenuMessage(content, customID, placeholder, current string, options []discordgo.SelectMenuOption) (string, []discordgo.MessageComponent) {
	minValues := 1
	return content, []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.SelectMenu{CustomID: customID, MenuType: discordgo.StringSelectMenu, Placeholder: placeholder, Options: options, MinValues: &minValues, MaxValues: 1},
	}}, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{CustomID: modelWizardBack, Style: discordgo.SecondaryButton, Label: "‹ Back"},
	}}}
}

func temperatureMenuMessage(current string) (string, []discordgo.MessageComponent) {
	if current == "" {
		current = "default"
	}
	options := make([]discordgo.SelectMenuOption, 0, len(modelTemperatures))
	for _, value := range modelTemperatures {
		label := value
		if value == "default" {
			label = "Default"
		}
		options = append(options, discordgo.SelectMenuOption{Label: label, Value: value, Default: value == current})
	}
	return selectMenuMessage("Select a temperature:", modelWizardTemp, "Select temperature", current, options)
}

func thinkingMenuMessage(current string) (string, []discordgo.MessageComponent) {
	return selectMenuMessage("Select a thinking level:", modelWizardThinking, "Select thinking level", current, makeThinkingOptions(current))
}

func keyMenuMessage(count int, current string) (string, []discordgo.MessageComponent) {
	if count < 1 {
		count = 1
	}
	if count > 25 {
		count = 25
	}
	options := make([]discordgo.SelectMenuOption, 0, count)
	for index := 1; index <= count; index++ {
		value := strconv.Itoa(index)
		options = append(options, discordgo.SelectMenuOption{Label: "Pool " + value, Value: value, Default: value == current})
	}
	return selectMenuMessage("Select an API key pool:", modelWizardKey, "Select API key pool", current, options)
}

// wizardErrorMessage re-renders the failure inside the same message with a
// Back button, so one mis-tap never strands the user.
func wizardErrorMessage(message string) (string, []discordgo.MessageComponent) {
	return message, []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{CustomID: modelWizardBack, Style: discordgo.SecondaryButton, Label: "‹ Back"},
	}}}
}

// stepWizard serves every wizard component interaction behind a deferred
// message update, then rewrites the same channel message (REQ-024).
func (h *ModelSettingsHandler) stepWizard(s interactionAPI, i *discordgo.InteractionCreate) error {
	data := i.MessageComponentData()
	userID := interactionUserID(i)
	if userID == "" {
		return h.respondError(s, i, "could not tell who invoked the menu")
	}
	wizard, ok := h.wizardFor(i.ChannelID, userID)
	if !ok {
		return h.respondError(s, i, "this setup expired; run /model again")
	}
	if err := deferMessageUpdate(s, i); err != nil {
		return err
	}
	content, components, done, err := h.advance(i, wizard, data)
	if err != nil {
		content, components = wizardErrorMessage("Model settings error: " + err.Error())
		return editOriginalMessage(s, i, content, components)
	}
	if done {
		h.dropWizard(i.ChannelID, userID)
	}
	return editOriginalMessage(s, i, content, components)
}

// advance applies one wizard interaction and renders the next step. It
// returns done=true when the settings were applied and the content is the
// final summary.
func (h *ModelSettingsHandler) advance(i *discordgo.InteractionCreate, wizard *modelWizard, data discordgo.MessageComponentInteractionData) (string, []discordgo.MessageComponent, bool, error) {
	choice := ""
	if len(data.Values) > 0 {
		choice = strings.TrimSpace(data.Values[0])
	}
	switch data.CustomID {
	case modelWizardProvider:
		return h.pickProvider(i, wizard, choice)
	case modelWizardPagePrev:
		wizard.page--
		return h.renderModelPage(i, wizard)
	case modelWizardPageNext:
		wizard.page++
		return h.renderModelPage(i, wizard)
	case modelWizardModel:
		return h.pickModel(i, wizard, choice)
	case modelWizardTemp:
		return h.pickTemperature(wizard, choice)
	case modelWizardThinking:
		return h.pickThinking(wizard, choice)
	case modelWizardKey:
		return h.pickKey(i, wizard, choice)
	case modelWizardBack:
		return h.goBack(i, wizard)
	default:
		return "", nil, false, fmt.Errorf("unknown menu")
	}
}

func (h *ModelSettingsHandler) pickProvider(i *discordgo.InteractionCreate, wizard *modelWizard, choice string) (string, []discordgo.MessageComponent, bool, error) {
	provider := sdk.ProviderID(choice)
	if provider == "" {
		return "", nil, false, fmt.Errorf("provider is required")
	}
	if !h.hasProvider(provider) {
		return "", nil, false, fmt.Errorf("unknown provider %q", provider)
	}
	if h.ProviderKeys[provider] == nil {
		return "", nil, false, fmt.Errorf("provider %q has no API key pool", provider)
	}
	wizard.provider = provider
	wizard.model = ""
	wizard.page = 0
	wizard.stage = modelStageModel
	return h.renderModelPage(i, wizard)
}

func (h *ModelSettingsHandler) renderModelPage(i *discordgo.InteractionCreate, wizard *modelWizard) (string, []discordgo.MessageComponent, bool, error) {
	if h.Models == nil {
		return "", nil, false, fmt.Errorf("model catalogue loader is not configured")
	}
	models, err := h.Models(context.Background(), wizard.provider)
	if err != nil {
		return "", nil, false, err
	}
	if len(models) == 0 {
		return "", nil, false, fmt.Errorf("provider %q has no models", wizard.provider)
	}
	current := ""
	if session, err := h.resolve(i); err == nil {
		current = session.Config().Model
	}
	content, components := modelPageMessage(wizard.provider, models, wizard.page, current)
	return content, components, false, nil
}

func (h *ModelSettingsHandler) pickModel(i *discordgo.InteractionCreate, wizard *modelWizard, choice string) (string, []discordgo.MessageComponent, bool, error) {
	if choice == "" {
		return "", nil, false, fmt.Errorf("model is required")
	}
	if h.Models == nil {
		return "", nil, false, fmt.Errorf("model catalogue loader is not configured")
	}
	models, err := h.Models(context.Background(), wizard.provider)
	if err != nil {
		return "", nil, false, err
	}
	if !modelInCatalog(models, choice) {
		return "", nil, false, fmt.Errorf("model %q is not available for provider %q", choice, wizard.provider)
	}
	wizard.model = choice
	wizard.stage = modelStageTemp
	current := wizard.temperature
	if session, err := h.resolve(i); err == nil {
		current = temperatureLabel(session.Config().Temperature)
	}
	content, components := temperatureMenuMessage(current)
	return content, components, false, nil
}

func (h *ModelSettingsHandler) pickTemperature(wizard *modelWizard, choice string) (string, []discordgo.MessageComponent, bool, error) {
	valid := false
	for _, preset := range modelTemperatures {
		if choice == preset {
			valid = true
			break
		}
	}
	if !valid {
		return "", nil, false, fmt.Errorf("temperature must be one of the offered presets")
	}
	wizard.temperature = choice
	wizard.stage = modelStageThinking
	content, components := thinkingMenuMessage(wizard.thinking)
	return content, components, false, nil
}

func (h *ModelSettingsHandler) pickThinking(wizard *modelWizard, choice string) (string, []discordgo.MessageComponent, bool, error) {
	choice = strings.ToLower(choice)
	if choice != "default" && !validDiscordThinkingLevel(choice) {
		return "", nil, false, fmt.Errorf("invalid thinking level %q", choice)
	}
	wizard.thinking = choice
	wizard.stage = modelStageKey
	count := 1
	if keys := h.ProviderKeys[wizard.provider]; keys != nil && keys.Len() > 0 {
		count = keys.Len()
	}
	content, components := keyMenuMessage(count, wizard.key)
	return content, components, false, nil
}

func (h *ModelSettingsHandler) pickKey(i *discordgo.InteractionCreate, wizard *modelWizard, choice string) (string, []discordgo.MessageComponent, bool, error) {
	index, err := strconv.Atoi(choice)
	if err != nil || index < 1 {
		return "", nil, false, fmt.Errorf("API Pool must be a positive number")
	}
	keys := h.ProviderKeys[wizard.provider]
	if keys == nil {
		return "", nil, false, fmt.Errorf("provider %q has no API key pool", wizard.provider)
	}
	if _, err := keys.At(index - 1); err != nil {
		return "", nil, false, err
	}
	wizard.key = choice
	session, err := h.resolve(i)
	if err != nil {
		return "", nil, false, err
	}
	values := map[string]string{"model": wizard.model, "temperature": wizard.temperature, "thinking": wizard.thinking, "key": wizard.key}
	var models []sdk.Model
	if h.Models != nil {
		models, err = h.Models(context.Background(), wizard.provider)
		if err != nil {
			return "", nil, false, err
		}
	}
	if err := applyModelSetup(session, keys, wizard.provider, values, models); err != nil {
		return "", nil, false, err
	}
	return sessionSettingsSummary(session.Config()), nil, true, nil
}

// goBack returns the wizard one stage, re-rendering from the stored choices.
func (h *ModelSettingsHandler) goBack(i *discordgo.InteractionCreate, wizard *modelWizard) (string, []discordgo.MessageComponent, bool, error) {
	switch wizard.stage {
	case modelStageModel:
		wizard.stage = modelStageProvider
		content, components := providerMenuMessage(h.Providers)
		return content, components, false, nil
	case modelStageTemp:
		wizard.stage = modelStageModel
		return h.renderModelPage(i, wizard)
	case modelStageThinking:
		wizard.stage = modelStageTemp
		return h.renderTempPage(i, wizard)
	case modelStageKey:
		wizard.stage = modelStageThinking
		content, components := thinkingMenuMessage(wizard.thinking)
		return content, components, false, nil
	default:
		wizard.stage = modelStageProvider
		content, components := providerMenuMessage(h.Providers)
		return content, components, false, nil
	}
}

func (h *ModelSettingsHandler) renderTempPage(i *discordgo.InteractionCreate, wizard *modelWizard) (string, []discordgo.MessageComponent, bool, error) {
	current := wizard.temperature
	if session, err := h.resolve(i); err == nil {
		current = temperatureLabel(session.Config().Temperature)
	}
	content, components := temperatureMenuMessage(current)
	return content, components, false, nil
}

func (h *ModelSettingsHandler) resolve(i *discordgo.InteractionCreate) (*sdk.Session, error) {
	if h.ResolveSession == nil {
		return nil, fmt.Errorf("session manager is not configured")
	}
	return h.ResolveSession(context.Background(), sdk.Input{SessionID: h.sessionIDFor(i.ChannelID)})
}

// applyModelSetup validates one setup submission and applies it to the
// session. A blank key field keeps the session's current pool index.
func applyModelSetup(session *sdk.Session, keys *sdk.KeyPool, provider sdk.ProviderID, values map[string]string, models []sdk.Model) error {
	if session == nil {
		return fmt.Errorf("session manager is not configured")
	}
	if keys == nil {
		return fmt.Errorf("provider %q has no API key pool", provider)
	}
	model := strings.TrimSpace(values["model"])
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
func boolPtr(value bool)*bool{return &value};func intPtr(value int)*int{return &value}
func makeProviderOptions(providers []sdk.ProviderID, current string) []discordgo.SelectMenuOption { options:=make([]discordgo.SelectMenuOption,0,min(len(providers),25));for _,provider:=range providers{value:=string(provider);if value==""||len(options)>=25{continue};options=append(options,discordgo.SelectMenuOption{Label:value,Value:value,Default:value==current})};return options }
func makeModelOptionGroups(models []sdk.Model, current string) [][]discordgo.SelectMenuOption { all:=make([]discordgo.SelectMenuOption,0,len(models));for _,model:=range models{if model.ID==""{continue};all=append(all,discordgo.SelectMenuOption{Label:model.ID,Value:model.ID,Default:model.ID==current})};groups:=make([][]discordgo.SelectMenuOption,0,(len(all)+24)/25);for start:=0;start<len(all);start+=25{end:=start+25;if end>len(all){end=len(all)};group:=make([]discordgo.SelectMenuOption,end-start);copy(group,all[start:end]);groups=append(groups,group)};return groups }
func makeTemperatureOptions(current string) []discordgo.SelectMenuOption { return []discordgo.SelectMenuOption{{Label:current,Value:current,Default:true}} }
func makeThinkingOptions(current string) []discordgo.SelectMenuOption {values:=[]string{"default",string(sdk.ThinkingNone),string(sdk.ThinkingLow),string(sdk.ThinkingMedium),string(sdk.ThinkingHigh)};options:=make([]discordgo.SelectMenuOption,0,len(values));for _,value:=range values{label:=value;if value=="default"{label="Default"};options=append(options,discordgo.SelectMenuOption{Label:label,Value:value,Default:value==current||(current==""&&value=="default")})};return options }
func makeKeyOptions(count int,current string) []discordgo.SelectMenuOption {if count<1{count=1};if count>25{count=25};currentIndex,_:=strconv.Atoi(current);options:=make([]discordgo.SelectMenuOption,0,count);for index:=1;index<=count;index++{value:=strconv.Itoa(index);options=append(options,discordgo.SelectMenuOption{Label:"Pool "+value,Value:value,Default:index==currentIndex})};if currentIndex<1||currentIndex>count{options[0].Default=true};return options }
func(h *ModelSettingsHandler)respondError(s interactionAPI,i *discordgo.InteractionCreate,message string)error{data:=&discordgo.InteractionResponseData{Content:"Model settings error: "+message,Flags:discordgo.MessageFlagsEphemeral};return s.InteractionRespond(i.Interaction,&discordgo.InteractionResponse{Type:discordgo.InteractionResponseChannelMessageWithSource,Data:data})}
