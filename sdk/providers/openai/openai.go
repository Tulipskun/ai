package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"os"

	"github.com/Tulipskun/ai/sdk"
	"github.com/Tulipskun/ai/sdk/providers/internal"
)

type Client struct { BaseURL, APIKey string; HTTP *http.Client }
func New(apiKey string) *Client { if apiKey == "" { apiKey = os.Getenv("OPENAI_API_KEY") }; return &Client{BaseURL: "https://api.openai.com/v1", APIKey: apiKey, HTTP: http.DefaultClient} }
func (c *Client) WithAPIKey(key string) sdk.Provider { cp:=*c; cp.APIKey=key; return &cp }
func (c *Client) WithBaseURL(baseURL string) sdk.Provider { cp:=*c; cp.BaseURL=baseURL; return &cp }
func (c *Client) ListModels(ctx context.Context, apiKey string) ([]sdk.Model,error) { if apiKey!="" { c=c.WithAPIKey(apiKey).(*Client) }; var r struct{Data []struct{ID string `json:"id"`} `json:"data"`}; if err:=internal.DoJSON(ctx,c.http(),http.MethodGet,c.BaseURL+"/models",c.headers(),nil,&r);err!=nil{return nil,err}; models:=make([]sdk.Model,0,len(r.Data));for _,item:=range r.Data{if item.ID!=""{models=append(models,sdk.Model{ID:item.ID,Name:item.ID,SupportsStreaming:true,SupportsTemperature:true})}};return models,nil }
func (c *Client) Name() string { return "openai" }

type response struct {
	ID string `json:"id"`
	Model string `json:"model"`
	Output []struct {
		Type string `json:"type"`
		ID string `json:"id"`
		CallID string `json:"call_id"`
		Name string `json:"name"`
		Arguments string `json:"arguments"`
		Content []struct { Type string `json:"type"`; Text string `json:"text"` } `json:"content"`
	} `json:"output"`
	Status string `json:"status"`
	Usage struct { InputTokens int `json:"input_tokens"`; OutputTokens int `json:"output_tokens"`; TotalTokens int `json:"total_tokens"`; InputDetails struct { Cached int `json:"cached_tokens"` } `json:"input_tokens_details"` } `json:"usage"`
}

func build(req sdk.Request) map[string]any {
	b:=map[string]any{"model":req.Model,"stream":req.Stream}; if req.SystemPrompt!=""{b["instructions"]=req.SystemPrompt}; var input []any
	for _,m:=range req.Messages {
		switch m.Role {
		case sdk.RoleUser,sdk.RoleModel:
			if m.Reasoning!=nil && m.Reasoning.Text!="" { input=append(input,map[string]any{"type":"reasoning","id":"reasoning_"+stableReasoningID(m.Reasoning.Text),"status":"completed","content":[]any{map[string]any{"type":"reasoning_text","text":m.Reasoning.Text}}}) }
			text:="";for _,p:=range m.Content{text+=p.Text};if text!=""{role:=string(m.Role);if m.Role==sdk.RoleModel{role="assistant"};input=append(input,map[string]any{"type":"message","role":role,"content":text})}
		case sdk.RoleToolCall:
			if m.ToolCall!=nil{input=append(input,map[string]any{"type":"function_call","call_id":m.ToolCall.ID,"name":m.ToolCall.Name,"arguments":m.ToolCall.Arguments})}
		case sdk.RoleToolResult:
			if m.ToolResult!=nil{input=append(input,map[string]any{"type":"function_call_output","call_id":m.ToolResult.ID,"output":m.ToolResult.Content})}
		}
	}; b["input"]=input
	if len(req.Tools)>0{tools:=make([]any,0,len(req.Tools));for _,t:=range req.Tools{tools=append(tools,map[string]any{"type":"function","name":t.Name,"description":t.Description,"parameters":t.InputSchema})};b["tools"]=tools}
	if req.Temperature!=nil{b["temperature"]=*req.Temperature};if req.MaxOutputTokens>0{b["max_output_tokens"]=req.MaxOutputTokens};if req.ThinkingLevel!=""&&req.ThinkingLevel!=sdk.ThinkingNone{b["reasoning"]=map[string]any{"effort":string(req.ThinkingLevel)}};return b
}

func stableReasoningID(text string) string { var h uint64=1469598103934665603;for i:=0;i<len(text);i++{h^=uint64(text[i]);h*=1099511628211};return string(rune('a'+(h%26))) }

func parseResponse(r response) sdk.Response { out:=sdk.Response{Provider:"openai",Model:r.Model,Usage:sdk.Usage{InputTokens:r.Usage.InputTokens,OutputTokens:r.Usage.OutputTokens,TotalTokens:r.Usage.TotalTokens,CacheReadTokens:r.Usage.InputDetails.Cached},Cache:sdk.CacheInfo{Layer:"provider"}};out.Cache.Hit=out.Usage.CacheReadTokens>0;for _,item:=range r.Output{switch item.Type{case "reasoning": for _,p:=range item.Content{if p.Type=="reasoning_text"&&p.Text!=""{out.Reasoning=&sdk.ReasoningState{Text:p.Text};break}};case "message":for _,p:=range item.Content{if p.Text!=""{out.Content=append(out.Content,sdk.ContentPart{Type:sdk.ContentText,Text:p.Text})}};case "function_call":out.ToolCalls=append(out.ToolCalls,sdk.ToolCall{ID:item.CallID,Name:item.Name,Arguments:item.Arguments})}};out.FinishReason=r.Status;return out }
func (c *Client) headers() map[string]string{return map[string]string{"Authorization":"Bearer "+c.APIKey}}
func (c *Client) Generate(ctx context.Context,req sdk.Request)(sdk.Response,error){var r response;if err:=internal.DoJSON(ctx,c.http(),http.MethodPost,c.BaseURL+"/responses",c.headers(),build(req),&r);err!=nil{return sdk.Response{},err};return parseResponse(r),nil}
func (c *Client) Stream(ctx context.Context,req sdk.Request)(<-chan sdk.Event,error){req.Stream=true;ch:=make(chan sdk.Event,16);go func(){defer close(ch);err:=internal.SSE(ctx,c.http(),http.MethodPost,c.BaseURL+"/responses",c.headers(),build(req),func(data []byte)error{var e struct{Type string `json:"type"`;Delta string `json:"delta"`;Item struct{CallID string `json:"call_id"`;Name string `json:"name"`;Arguments string `json:"arguments"`} `json:"item"`};if json.Unmarshal(data,&e)!=nil{return nil};switch e.Type{case "response.output_text.delta":if e.Delta!=""{ch<-sdk.Event{Type:sdk.EventText,Text:e.Delta}};case "response.function_call_arguments.done":if e.Item.CallID!=""{ch<-sdk.Event{Type:sdk.EventToolCall,ToolCall:&sdk.ToolCall{ID:e.Item.CallID,Name:e.Item.Name,Arguments:e.Item.Arguments}}};case "response.completed":ch<-sdk.Event{Type:sdk.EventDone}};return nil});if err!=nil{ch<-sdk.Event{Type:sdk.EventError,Err:err}}}();return ch,nil}
func (c *Client) http()*http.Client{if c.HTTP!=nil{return c.HTTP};return http.DefaultClient}
