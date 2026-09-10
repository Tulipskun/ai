package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tulipskun/ai/sdk"
)

type InputMessage struct { SessionID string; ChannelID string; MessageID string; AuthorID string; AuthorName string; Content string }
func ToInput(message InputMessage) sdk.Input { return sdk.Input{Source:"discord",SessionID:message.SessionID,Turn:sdk.Turn{Role:sdk.RoleUser,Content:[]sdk.ContentPart{{Type:sdk.ContentText,Text:message.Content}}},Metadata:map[string]string{"channel_id":message.ChannelID,"message_id":message.MessageID,"author_id":message.AuthorID,"author_name":message.AuthorName}} }
type InputSource struct { Messages <-chan InputMessage }
func(s InputSource)Receive(ctx context.Context)(<-chan sdk.Input,error){if s.Messages==nil{return nil,errors.New("discord: input source has no message channel")};out:=make(chan sdk.Input);go func(){defer close(out);for{select{case<-ctx.Done():return;case message,ok:=<-s.Messages:if !ok{return};select{case out<-ToInput(message):case<-ctx.Done():return}}}}();return out,nil}
type Sender interface{SendMessage(context.Context,string,string)error}
type RetryStatusSender interface{SendStatusMessage(context.Context,string,string)(string,error);EditMessage(context.Context,string,string,string)error;DeleteMessage(context.Context,string,string)error}
type toolTraceSender interface{setToolTrace(context.Context,string,[]string)error;appendToolTrace(context.Context,string,string)error;clearToolTrace(context.Context,string)error}
type textTraceSender interface{appendTextTrace(context.Context,string,string)error}
type retryStatusManager interface{RetryStatusSender;updateRetryStatus(context.Context,string,string)error;clearRetryStatus(context.Context,string)error}
type Display struct{Sender Sender}
func(d Display)Source()string{return "discord"}
func(d Display)Display(ctx context.Context,output sdk.Output)error{if d.Sender==nil{return errors.New("discord: display has no sender")};channelID:=output.Metadata["channel_id"];if channelID==""{return errors.New("discord: output has no channel_id")};if output.Trace!=nil{return d.displayTrace(ctx,channelID,*output.Trace)};data,err:=json.Marshal(output);if err!=nil{return err};text:=string(data);for _,chunk:=range discordChunks(text,1900){if err:=d.Sender.SendMessage(ctx,channelID,chunk);err!=nil{return err}};return nil}
func(d Display)displayTrace(ctx context.Context,channelID string,trace sdk.TraceEvent)error{sender,hasTrace:=d.Sender.(toolTraceSender);message:=sdk.TraceMessage(trace);switch trace.Stage{case sdk.TraceRequest,sdk.TraceProviderReady,sdk.TraceResponseText:if !hasTrace{return nil};text:=message;if trace.Text!=""{text += ": "+truncateOneLine(trace.Text,maxToolTraceLength)};if trace.Stage==sdk.TraceResponseText&&trace.Text!=""{text=message+": "+truncateOneLine(trace.Text,maxToolTraceLength)};if trace.Stage==sdk.TraceProviderReady{text=withElapsed(text,trace.Elapsed)};return sender.appendToolTrace(ctx,channelID,text);case sdk.TraceResponseContent:text:=strings.TrimSpace(trace.Text);if text==""{text=responseContent(trace.Response)};if text==""{return nil};if textSender,ok:=d.Sender.(textTraceSender);ok{return textSender.appendTextTrace(ctx,channelID,text)};if toolSender,ok:=d.Sender.(toolTraceSender);ok{return toolSender.appendToolTrace(ctx,channelID,text)};for _,chunk:=range discordChunks(text,1900){if err:=d.Sender.SendMessage(ctx,channelID,chunk);err!=nil{return err}};return nil;case sdk.TraceToolCall:if trace.ToolCall==nil||!hasTrace{return nil};return sender.appendToolTrace(ctx,channelID,withElapsed(formatToolTraceCall(trace.ToolCall),trace.Elapsed));case sdk.TraceToolRunning:if trace.ToolCall==nil||!hasTrace{return nil};return sender.appendToolTrace(ctx,channelID,withElapsed(message+": "+formatToolTraceCall(trace.ToolCall),trace.Elapsed));case sdk.TraceToolResult:if !hasTrace||trace.ToolResult==nil{return nil};return sender.appendToolTrace(ctx,channelID,withElapsed(message+": "+formatToolTraceCall(trace.ToolCall),trace.Elapsed));case sdk.TraceResponse:if status,ok:=d.Sender.(retryStatusManager);ok{if err:=status.clearRetryStatus(ctx,channelID);err!=nil{return err}};return nil;case sdk.TraceRetryWait:var text string;if trace.Err!=nil&&trace.RetryAfter>0{text=fmt.Sprintf("[AI retry] %s\n%s: %s",trace.Err,message,formatDuration(trace.RetryAfter))}else if trace.Err!=nil{text=fmt.Sprintf("[AI retry] %s\n%s",trace.Err,message)};if status,ok:=d.Sender.(retryStatusManager);ok{return status.updateRetryStatus(ctx,channelID,text)};case sdk.TraceError:default:return nil};return nil}
const maxToolTraceLength=500
const maxTextTraceLength=4000
func withElapsed(text string,elapsed time.Duration)string{if elapsed<=0{return text};return text+" · "+formatDuration(elapsed)}
func truncateText(text string,max int)string{if max<=0{return text};runes:=[]rune(text);if len(runes)<=max{return text};return string(runes[:max-1])+"…"}
func formatToolTraceCall(call *sdk.ToolCall)string{if call==nil{return "tool()"};args:=compactToolArguments(call.Arguments);if args==""{return truncateOneLine(fmt.Sprintf("%s()",call.Name),maxToolTraceLength)};return truncateOneLine(fmt.Sprintf("%s(%q)",call.Name,args),maxToolTraceLength)}
func formatToolCall(call *sdk.ToolCall)string{return formatToolTraceCall(call)}
func formatToolResult(call *sdk.ToolCall,result *sdk.ToolResult)string{if result==nil{return ""};prefix:="✅ ";if result.IsError{prefix="❌ "};return truncateOneLine(prefix+formatToolTraceCall(call),maxToolTraceLength)}
func compactToolArguments(arguments string)string{arguments=strings.TrimSpace(arguments);if arguments==""{return ""};var value any;if json.Unmarshal([]byte(arguments),&value)==nil{if data,err:=json.Marshal(value);err==nil{return string(data)}};return oneLine(arguments)}
func truncateOneLine(text string,max int)string{text=oneLine(text);if max<=0||len([]rune(text))<=max{return text};runes:=[]rune(text);return string(runes[:max-1])+"…"}
func oneLine(text string)string{return strings.Join(strings.Fields(text)," ")}
func formatDuration(d time.Duration)string{if d<time.Second{return d.Round(time.Millisecond).String()};return d.Round(time.Second).String()}
func responseContent(response *sdk.Response)string{if response==nil{return ""};var b strings.Builder;for _,part:=range response.Content{if part.Type==sdk.ContentText{b.WriteString(part.Text)}};return b.String()}
func discordChunks(text string,max int)[]string{if len(text)<=max{return []string{text}};chunks:=make([]string,0,(len(text)+max-1)/max);for len(text)>max{cut:=strings.LastIndexByte(text[:max],'\n');if cut<=0{cut=max};chunks=append(chunks,text[:cut]);text=strings.TrimLeft(text[cut:],"\n")};if text!=""{chunks=append(chunks,text)};return chunks}
