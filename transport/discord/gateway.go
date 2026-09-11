package discord

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

type discordMessage struct { ID string; ChannelID string; AuthorID string; AuthorName string; Content string; AuthorIsBot bool }
func normalizeMessage(message discordMessage)(InputMessage,bool){if message.AuthorIsBot||message.ID==""||message.ChannelID==""||message.AuthorID==""{return InputMessage{},false};return InputMessage{SessionID:"discord:channel:"+message.ChannelID,ChannelID:message.ChannelID,MessageID:message.ID,AuthorID:message.AuthorID,AuthorName:message.AuthorName,Content:message.Content},true}
func gatewayIntents()discordgo.Intent{return discordgo.IntentsGuildMessages|discordgo.IntentsDirectMessages|discordgo.IntentsMessageContent}
type toolTraceState struct{messageID string;items []string;isText bool;dirty bool;lastPush time.Time;flushTimer *time.Timer}
const maxEmbedChars=4000
// traceFlushInterval bounds embed updates to one snapshot per second so bursts of
// trace events stay under Discord's message-edit rate limits.
const traceFlushInterval=time.Second
func embedChars(items []string)int{n:=0;for i,w:=range items{if i>0{n++};n+=len([]rune(w))};return n}
func(s *toolTraceState)append(item string,isText bool){
if isText&&s.isText&&len(s.items)>0{s.items[len(s.items)-1]+=item;s.items[len(s.items)-1]=truncateText(s.items[len(s.items)-1],maxTextTraceLength)}else{s.items=append(s.items,item)}
s.isText=isText
for len(s.items)>1&&embedChars(s.items)>maxEmbedChars{s.items=s.items[1:]}
}
// update rewrites the newest line that match identifies so live statuses (request
// accepted, tool result) reuse their placeholder line instead of stacking lines.
func(s *toolTraceState)update(item string,match func(string)bool)bool{
if match==nil{return false}
for i:=len(s.items)-1;i>=0;i--{if match(s.items[i]){s.items[i]=item;for len(s.items)>1&&embedChars(s.items)>maxEmbedChars{s.items=s.items[1:]};return true}}
return false
}
// reset drops the current lines so the next append starts a fresh embed.
func(s *toolTraceState)reset(){s.messageID="";s.items=nil}
func traceFlushDelay(lastPush time.Time)time.Duration{if delay:=traceFlushInterval-time.Since(lastPush);delay>0{return delay};return 0}
type Gateway struct{session *discordgo.Session;messages chan InputMessage;done chan struct{};closeMu sync.Mutex;closed bool;modelSettings *ModelSettingsHandler;providerSettings *ProviderSettingsHandler;sessionCommand *SessionCommandHandler;sessionMapping *SessionMapping;stop func(string)bool;retryStatusMu sync.Mutex;retryStatus map[string]string;authorizedUserID string;toolTraceMu sync.Mutex;toolTrace map[string]*toolTraceState}
func NewGateway(token string)(*Gateway,error){if strings.TrimSpace(token)==""{return nil,errors.New("discord: bot token is required")};session,err:=discordgo.New("Bot "+token);if err!=nil{return nil,err};gateway:=&Gateway{session:session,messages:make(chan InputMessage,256),done:make(chan struct{}),retryStatus:make(map[string]string),toolTrace:make(map[string]*toolTraceState),sessionMapping:NewSessionMapping()};session.Identify.Intents=gatewayIntents();session.AddHandler(func(_ *discordgo.Session,event *discordgo.MessageCreate){if event==nil||event.Author==nil{return};message,ok:=normalizeMessage(discordMessage{ID:event.ID,ChannelID:event.ChannelID,AuthorID:event.Author.ID,AuthorName:event.Author.Username,Content:event.Content,AuthorIsBot:event.Author.Bot});if !ok{return};message.SessionID=gateway.SessionIDForChannel(message.ChannelID);gateway.resetToolTrace(message.ChannelID);if gateway.stop!=nil{_ = gateway.stop(message.SessionID)};select{case gateway.messages<-message:case<-gateway.done:}});session.AddHandler(func(s *discordgo.Session,event *discordgo.InteractionCreate){if event==nil{return};if gateway.authorizedUserID!=""&&event.Member!=nil&&event.Member.User!=nil&&event.Member.User.ID!=gateway.authorizedUserID{_=s.InteractionRespond(event.Interaction,&discordgo.InteractionResponse{Type:discordgo.InteractionResponseChannelMessageWithSource,Data:&discordgo.InteractionResponseData{Content:"Unauthorized",Flags:discordgo.MessageFlagsEphemeral}});return};if gateway.authorizedUserID!=""&&event.Member==nil&&event.User!=nil&&event.User.ID!=gateway.authorizedUserID{_=s.InteractionRespond(event.Interaction,&discordgo.InteractionResponse{Type:discordgo.InteractionResponseChannelMessageWithSource,Data:&discordgo.InteractionResponseData{Content:"Unauthorized",Flags:discordgo.MessageFlagsEphemeral}});return};if data,ok:=event.Interaction.Data.(discordgo.ApplicationCommandInteractionData);ok&&data.Name=="stop"&&gateway.stop!=nil{content:="ไม่มีงานที่กำลังทำอยู่";if gateway.stop(gateway.SessionIDForChannel(event.ChannelID)){content="หยุดการทำงานแล้ว"};_=s.InteractionRespond(event.Interaction,&discordgo.InteractionResponse{Type:discordgo.InteractionResponseChannelMessageWithSource,Data:&discordgo.InteractionResponseData{Content:content,Flags:discordgo.MessageFlagsEphemeral}});return};if gateway.sessionCommand!=nil{if err:=gateway.sessionCommand.Handle(s,event);err!=nil{return};if data,ok:=event.Interaction.Data.(discordgo.ApplicationCommandInteractionData);ok&&data.Name=="session"{return}};if gateway.modelSettings!=nil{if err:=gateway.modelSettings.Handle(s,event);err!=nil&&event.Type==discordgo.InteractionApplicationCommand&&event.ApplicationCommandData().Name=="model"{return}};if gateway.providerSettings!=nil{_=gateway.providerSettings.Handle(s,event)}});return gateway,nil}
func(g *Gateway)ConfigureAuthorizedUser(userID string){if g!=nil{g.authorizedUserID=strings.TrimSpace(userID)}}
func(g *Gateway)ConfigureModelSettings(handler *ModelSettingsHandler){if g!=nil{g.modelSettings=handler}}
func(g *Gateway)ConfigureProviderSettings(handler *ProviderSettingsHandler){if g!=nil{g.providerSettings=handler}}
func(g *Gateway)ConfigureSessionCommand(handler *SessionCommandHandler){if g!=nil{g.sessionCommand=handler}}
func(g *Gateway)ConfigureSessionList(list func(int)([]sdk.SessionInfo,error)){if g!=nil{g.sessionCommand=&SessionCommandHandler{ListSessions:list,SelectSession:g.SetChannelSession}}}
func(g *Gateway)ConfigureSessionMapping(mapping *SessionMapping){if g!=nil&&mapping!=nil{g.sessionMapping=mapping}}
func(g *Gateway)SetChannelSession(channelID,sessionID string)error{if g==nil{return errors.New("discord: gateway is nil")};if g.sessionMapping==nil{g.sessionMapping=NewSessionMapping()};return g.sessionMapping.Set(channelID,sessionID)}
func(g *Gateway)SessionIDForChannel(channelID string)string{if g==nil||g.sessionMapping==nil{return "discord:channel:"+strings.TrimSpace(channelID)};return g.sessionMapping.SessionID(channelID)}
func(g *Gateway)ConfigureStop(handler func(string)bool){if g!=nil{g.stop=handler}}
func(g *Gateway)Start(ctx context.Context)error{if g==nil||g.session==nil{return errors.New("discord: gateway is not initialized")};if g.authorizedUserID==""{return errors.New("discord: authorized user ID is required")};if err:=g.session.Open();err!=nil{return err};if g.modelSettings!=nil{if err:=g.registerCommand(&discordgo.ApplicationCommand{Name:"model",Description:"Configure the AI model for this session"});err!=nil{return err}};if g.providerSettings!=nil{if err:=g.registerCommand(&discordgo.ApplicationCommand{Name:"provider",Description:"Add or update an AI provider"});err!=nil{return err}};if g.sessionCommand!=nil{if err:=g.registerCommand(&discordgo.ApplicationCommand{Name:"session",Description:"Select an AI session"});err!=nil{return err}};if g.stop!=nil{if err:=g.registerCommand(&discordgo.ApplicationCommand{Name:"stop",Description:"Stop the current AI task"});err!=nil{return err}};go func(){<-ctx.Done();_=g.Close(context.Background())}();return nil}
func(g *Gateway)registerCommand(command *discordgo.ApplicationCommand)error{commands,err:=g.session.ApplicationCommands(g.session.State.User.ID,"");if err!=nil{return err};for _,existing:=range commands{if existing.Name==command.Name{_,err=g.session.ApplicationCommandEdit(g.session.State.User.ID,"",existing.ID,command);return err}};_,err=g.session.ApplicationCommandCreate(g.session.State.User.ID,"",command);return err}
func(g *Gateway)Receive(ctx context.Context)(<-chan sdk.Input,error){if g==nil||g.messages==nil{return nil,errors.New("discord: input source is not initialized")};return InputSource{Messages:g.messages}.Receive(ctx)}
func(g *Gateway)SendMessage(ctx context.Context,channelID,content string)error{if g==nil||g.session==nil{return errors.New("discord: gateway is not initialized")};if err:=ctx.Err();err!=nil{return err};if channelID==""||content==""{return errors.New("discord: channel ID and content are required")};_,err:=g.session.ChannelMessageSend(channelID,content);return err}
func(g *Gateway)SendStatusMessage(ctx context.Context,channelID,content string)(string,error){if g==nil||g.session==nil{return "",errors.New("discord: gateway is not initialized")};if err:=ctx.Err();err!=nil{return "",err};message,err:=g.session.ChannelMessageSend(channelID,content);if err!=nil{return "",err};return message.ID,nil}
func(g *Gateway)EditMessage(ctx context.Context,channelID,messageID,content string)error{if g==nil||g.session==nil{return errors.New("discord: gateway is not initialized")};if err:=ctx.Err();err!=nil{return err};if channelID==""||messageID==""{return errors.New("discord: channel ID and message ID are required")};_,err:=g.session.ChannelMessageEdit(channelID,messageID,content);return err}
func(g *Gateway)DeleteMessage(ctx context.Context,channelID,messageID string)error{if g==nil||g.session==nil{return errors.New("discord: gateway is not initialized")};if err:=ctx.Err();err!=nil{return err};if channelID==""||messageID==""{return errors.New("discord: channel ID and message ID are required")};return g.session.ChannelMessageDelete(channelID,messageID)}
func(g *Gateway)SendEmbed(ctx context.Context,channelID string,embed *discordgo.MessageEmbed)(string,error){if g==nil||g.session==nil{return "",errors.New("discord: gateway is not initialized")};if err:=ctx.Err();err!=nil{return "",err};message,err:=g.session.ChannelMessageSendEmbed(channelID,embed);if err!=nil{return "",err};return message.ID,nil}
func(g *Gateway)EditEmbed(ctx context.Context,channelID,messageID string,embed *discordgo.MessageEmbed)error{if g==nil||g.session==nil{return errors.New("discord: gateway is not initialized")};if err:=ctx.Err();err!=nil{return err};if channelID==""||messageID==""{return errors.New("discord: channel ID and message ID are required")};_,err:=g.session.ChannelMessageEditEmbed(channelID,messageID,embed);return err}
func toolTraceEmbed(items []string)*discordgo.MessageEmbed{return &discordgo.MessageEmbed{Description:strings.Join(items,"\n")}}
func(g *Gateway)traceState(channelID string)*toolTraceState{state:=g.toolTrace[channelID];if state==nil{state=&toolTraceState{};g.toolTrace[channelID]=state};return state}
func(g *Gateway)setToolTrace(_ context.Context,channelID string,items []string)error{g.toolTraceMu.Lock();defer g.toolTraceMu.Unlock();state:=g.traceState(channelID);state.items=append([]string(nil),items...);state.isText=false;g.scheduleTraceFlushLocked(channelID,state);return nil}
func(g *Gateway)appendToolTrace(ctx context.Context,channelID,item string)error{return g.appendTraceItem(ctx,channelID,truncateOneLine(item,maxToolTraceLength),false)}
func(g *Gateway)appendTextTrace(ctx context.Context,channelID,text string)error{return g.appendTraceItem(ctx,channelID,truncateText(text,maxTextTraceLength),true)}
func(g *Gateway)updateToolTrace(ctx context.Context,channelID,item string,match func(string)bool)error{return g.updateTraceItem(ctx,channelID,truncateOneLine(item,maxToolTraceLength),false,match)}
func(g *Gateway)appendTraceItem(ctx context.Context,channelID,item string,isText bool)error{g.toolTraceMu.Lock();defer g.toolTraceMu.Unlock();state:=g.traceState(channelID);g.appendStateItemLocked(ctx,channelID,state,item,isText);g.scheduleTraceFlushLocked(channelID,state);return nil}
func(g *Gateway)updateTraceItem(ctx context.Context,channelID,item string,isText bool,match func(string)bool)error{g.toolTraceMu.Lock();defer g.toolTraceMu.Unlock();state:=g.traceState(channelID);if !state.isText&&state.update(item,match){g.scheduleTraceFlushLocked(channelID,state);return nil};g.appendStateItemLocked(ctx,channelID,state,item,isText);g.scheduleTraceFlushLocked(channelID,state);return nil}
func(g *Gateway)appendStateItemLocked(ctx context.Context,channelID string,state *toolTraceState,item string,isText bool){
if len(state.items)>0&&state.isText!=isText{g.pushToolTraceLocked(ctx,channelID,state);state.reset()}
state.append(item,isText)}
// scheduleTraceFlushLocked queues a snapshot of the current trace state; snapshots
// are pushed at most once per traceFlushInterval.
func(g *Gateway)scheduleTraceFlushLocked(channelID string,state *toolTraceState){state.dirty=true;if state.flushTimer!=nil{return};state.flushTimer=time.AfterFunc(traceFlushDelay(state.lastPush),func(){g.flushToolTrace(context.Background(),channelID)})}
// flushToolTrace pushes the current trace snapshot to Discord immediately.
func(g *Gateway)flushToolTrace(ctx context.Context,channelID string)error{if g==nil{return nil};g.toolTraceMu.Lock();defer g.toolTraceMu.Unlock();state:=g.toolTrace[channelID];if state==nil{return nil};return g.pushToolTraceLocked(ctx,channelID,state)}
func(g *Gateway)pushToolTraceLocked(ctx context.Context,channelID string,state *toolTraceState)error{
if state.flushTimer!=nil{state.flushTimer.Stop();state.flushTimer=nil}
if !state.dirty||len(state.items)==0{return nil}
embed:=toolTraceEmbed(state.items)
if state.messageID==""{id,err:=g.SendEmbed(ctx,channelID,embed);if err!=nil{return err};state.messageID=id}else if err:=g.EditEmbed(ctx,channelID,state.messageID,embed);err!=nil{if !isUnknownMessage(err){return err};id,sendErr:=g.SendEmbed(ctx,channelID,embed);if sendErr!=nil{return sendErr};state.messageID=id}
state.dirty=false;state.lastPush=time.Now();return nil}
func(g *Gateway)resetToolTrace(channelID string){if g==nil||strings.TrimSpace(channelID)==""{return};g.toolTraceMu.Lock();defer g.toolTraceMu.Unlock();delete(g.toolTrace,channelID)}
func isUnknownMessage(err error)bool{var restErr *discordgo.RESTError;if errors.As(err,&restErr){return restErr.Message!=nil&&restErr.Message.Code==10008};return false}
func(g *Gateway)clearToolTrace(_ context.Context,_ string)error{return nil}
func(g *Gateway)updateRetryStatus(ctx context.Context,channelID,content string)error{g.retryStatusMu.Lock();defer g.retryStatusMu.Unlock();if messageID:=g.retryStatus[channelID];messageID!=""{editErr:=g.EditMessage(ctx,channelID,messageID,content);if editErr==nil{return nil};if !isUnknownMessage(editErr){return editErr};delete(g.retryStatus,channelID)};messageID,err:=g.SendStatusMessage(ctx,channelID,content);if err!=nil{return err};g.retryStatus[channelID]=messageID;return nil}
func(g *Gateway)clearRetryStatus(ctx context.Context,channelID string)error{g.retryStatusMu.Lock();defer g.retryStatusMu.Unlock();messageID:=g.retryStatus[channelID];if messageID==""{return nil};if err:=g.DeleteMessage(ctx,channelID,messageID);err!=nil&&!isUnknownMessage(err){return err};delete(g.retryStatus,channelID);return nil}
func(g *Gateway)Display(ctx context.Context,output sdk.Output)error{return(Display{Sender:g}).Display(ctx,output)}
func(g *Gateway)Source()string{return "discord"}
func(g *Gateway)Close(_ context.Context)error{if g==nil||g.session==nil{return nil};g.closeMu.Lock();if g.closed{g.closeMu.Unlock();return nil};g.closed=true;close(g.done);g.closeMu.Unlock();return g.session.Close()}
