package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/Tulipskun/ai/sdk"
	"github.com/Tulipskun/ai/sdk/providers/internal"
)

type Client struct { BaseURL string; APIKey string; HTTP *http.Client }
func New(apiKey string)*Client{if apiKey==""{apiKey=os.Getenv("GEMINI_API_KEY")};return &Client{BaseURL:"https://generativelanguage.googleapis.com/v1beta",APIKey:apiKey,HTTP:http.DefaultClient}}
func(c *Client)Name()string{return "gemini"}

func build(req sdk.Request)map[string]any{
	b:=map[string]any{}
	if req.SystemPrompt!=""{b["systemInstruction"]=map[string]any{"parts":[]any{map[string]any{"text":req.SystemPrompt}}}}
	var contents []any
	for _,m:=range req.Messages{switch m.Role{
	case sdk.RoleUser,sdk.RoleModel:
		role:="user";if m.Role==sdk.RoleModel{role="model"};text:="";for _,p:=range m.Content{text+=p.Text};contents=append(contents,map[string]any{"role":role,"parts":[]any{map[string]any{"text":text}}})
	case sdk.RoleToolCall:
		if m.ToolCall!=nil{var args any;_=json.Unmarshal([]byte(m.ToolCall.Arguments),&args);contents=append(contents,map[string]any{"role":"model","parts":[]any{map[string]any{"functionCall":map[string]any{"name":m.ToolCall.Name,"args":args}}}})}
	case sdk.RoleToolResult:
		if m.ToolResult!=nil{contents=append(contents,map[string]any{"role":"user","parts":[]any{map[string]any{"functionResponse":map[string]any{"name":m.ToolResult.ID,"response":map[string]any{"content":m.ToolResult.Content}}}})}}
	}}
	b["contents"]=contents;cfg:=map[string]any{}
	if req.Temperature!=nil{cfg["temperature"]=*req.Temperature};if req.MaxOutputTokens>0{cfg["maxOutputTokens"]=req.MaxOutputTokens};if req.ThinkingLevel!=""&&req.ThinkingLevel!=sdk.ThinkingNone{cfg["thinkingConfig"]=map[string]any{"thinkingLevel":string(req.ThinkingLevel)}};if len(cfg)>0{b["generationConfig"]=cfg}
	if len(req.Tools)>0{fds:=make([]any,0,len(req.Tools));for _,t:=range req.Tools{fds=append(fds,map[string]any{"name":t.Name,"description":t.Description,"parameters":t.InputSchema})};b["tools"]=[]any{map[string]any{"functionDeclarations":fds}}}
	return b
}

type functionCall struct{Name string `json:"name"`;Args map[string]any `json:"args"`}
type part struct{Text string `json:"text"`;FunctionCall *functionCall `json:"functionCall"`}
type candidate struct{Content struct{Parts []part `json:"parts"`} `json:"content"`;FinishReason string `json:"finishReason"`}
type response struct{Candidates []candidate `json:"candidates"`;Usage struct{Prompt int `json:"promptTokenCount"`;Output int `json:"candidatesTokenCount"`;Total int `json:"totalTokenCount"`;Cached int `json:"cachedContentTokenCount"`} `json:"usageMetadata"`}
func(c *Client)endpoint(model string,stream bool)string{method:="generateContent";if stream{method="streamGenerateContent"};u:=fmt.Sprintf("%s/models/%s:%s",c.BaseURL,url.PathEscape(model),method);if stream{u+="?alt=sse"};return u}
func(c *Client)headers()map[string]string{return map[string]string{"x-goog-api-key":c.APIKey}}
func(c *Client)Generate(ctx context.Context,req sdk.Request)(sdk.Response,error){var r response;if err:=internal.DoJSON(ctx,c.http(),http.MethodPost,c.endpoint(req.Model,false),c.headers(),build(req),&r);err!=nil{return sdk.Response{},err};return parse(r,req.Model),nil}
func parse(r response,model string)sdk.Response{out:=sdk.Response{Provider:"gemini",Model:model,Usage:sdk.Usage{InputTokens:r.Usage.Prompt,OutputTokens:r.Usage.Output,TotalTokens:r.Usage.Total,CacheReadTokens:r.Usage.Cached},Cache:sdk.CacheInfo{Layer:"provider"}};out.Cache.Hit=out.Usage.CacheReadTokens>0;if len(r.Candidates)==0{return out};out.FinishReason=r.Candidates[0].FinishReason;for _,p:=range r.Candidates[0].Content.Parts{if p.Text!=""{out.Content=append(out.Content,sdk.ContentPart{Type:sdk.ContentText,Text:p.Text})};if p.FunctionCall!=nil{a,_:=json.Marshal(p.FunctionCall.Args);out.ToolCalls=append(out.ToolCalls,sdk.ToolCall{ID:p.FunctionCall.Name,Name:p.FunctionCall.Name,Arguments:string(a)})}};return out}
func(c *Client)Stream(ctx context.Context,req sdk.Request)(<-chan sdk.Event,error){req.Stream=true;ch:=make(chan sdk.Event,16);go func(){defer close(ch);err:=internal.SSE(ctx,c.http(),http.MethodPost,c.endpoint(req.Model,true),c.headers(),build(req),func(data []byte)error{var r response;if json.Unmarshal(data,&r)!=nil{return nil};if len(r.Candidates)==0{return nil};for _,p:=range r.Candidates[0].Content.Parts{if p.Text!=""{ch<-sdk.Event{Type:sdk.EventText,Text:p.Text}};if p.FunctionCall!=nil{a,_:=json.Marshal(p.FunctionCall.Args);ch<-sdk.Event{Type:sdk.EventToolCall,ToolCall:&sdk.ToolCall{ID:p.FunctionCall.Name,Name:p.FunctionCall.Name,Arguments:string(a)}}}};if r.Candidates[0].FinishReason!=""{ch<-sdk.Event{Type:sdk.EventDone}};return nil});if err!=nil{ch<-sdk.Event{Type:sdk.EventError,Err:err}}}();return ch,nil}
func(c *Client)http()*http.Client{if c.HTTP!=nil{return c.HTTP};return http.DefaultClient}
