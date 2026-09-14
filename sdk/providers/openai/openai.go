package openai

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	oai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/Tulipskun/ai/sdk"
	"time"
)
type Client struct { BaseURL string; APIKey string; Headers map[string]string; HTTP *http.Client }
func New(apiKey string)*Client{return &Client{BaseURL:"https://api.openai.com/v1",APIKey:apiKey,HTTP:http.DefaultClient}}
func(c *Client)WithAPIKey(key string)sdk.Provider{cp:=*c;cp.APIKey=key;return &cp}
func(c *Client)WithBaseURL(baseURL string)sdk.Provider{cp:=*c;cp.BaseURL=baseURL;return &cp}
func(c *Client)WithHeaders(headers map[string]string)sdk.Provider{cp:=*c;cp.Headers=cloneHeaders(headers);return &cp}
func(c *Client)ListModels(ctx context.Context,apiKey string)([]sdk.Model,error){var cancel context.CancelFunc; ctx, cancel = context.WithTimeout(ctx, 30*time.Second); defer cancel();if apiKey!=""{c=c.WithAPIKey(apiKey).(*Client)};rec:=newRecorder(c.http());client:=c.sdkClient(rec);page,err:=client.Models.List(ctx);if err!=nil{return nil,mapAPIError(err,rec)};models:=make([]sdk.Model,0,len(page.Data));for _,item:=range page.Data{if item.ID!=""{models=append(models,sdk.Model{ID:item.ID,Name:item.ID,SupportsTemperature:true})}};return models,nil}
func(c *Client)Name()string{return "openai"}
type outputContent struct{Type string `json:"type"`;Text string `json:"text"`}
type outputItem struct{Type string `json:"type"`;ID string `json:"id"`;CallID string `json:"call_id"`;Name string `json:"name"`;Arguments string `json:"arguments"`;Content []outputContent `json:"content"`}
type response struct{ID string `json:"id"`;Model string `json:"model"`;Output []outputItem `json:"output"`;Status string `json:"status"`;Usage struct{InputTokens int `json:"input_tokens"`;OutputTokens int `json:"output_tokens"`;TotalTokens int `json:"total_tokens"`;InputDetails struct{Cached int `json:"cached_tokens"`} `json:"input_tokens_details"`} `json:"usage"`}
type chatFunction struct{Name string `json:"name"`;Arguments string `json:"arguments"`}
type chatToolCall struct{ID string `json:"id"`;Function chatFunction `json:"function"`}
type chatMessage struct{Content string `json:"content"`;ToolCalls []chatToolCall `json:"tool_calls"`}
type chatChoice struct{Message chatMessage `json:"message"`;FinishReason string `json:"finish_reason"`}
type chatResponse struct{Model string `json:"model"`;Choices []chatChoice `json:"choices"`;Usage struct{PromptTokens int `json:"prompt_tokens"`;CompletionTokens int `json:"completion_tokens"`;TotalTokens int `json:"total_tokens"`} `json:"usage"`}
func BuildResponsesRequest(req sdk.Request) map[string]any {b:=map[string]any{"model":req.Model};if req.SystemPrompt!=""{b["instructions"]=req.SystemPrompt};var input []any;for _,m:=range req.Messages{switch m.Role{case sdk.RoleUser,sdk.RoleModel:if m.Reasoning!=nil&&m.Reasoning.Text!=""{item:=map[string]any{"type":"reasoning","status":"completed","content":[]any{map[string]any{"type":"reasoning_text","text":m.Reasoning.Text}}};if m.Reasoning.ID!=""{item["id"]=m.Reasoning.ID};input=append(input,item)};text:="";for _,p:=range m.Content{text+=p.Text};if text!=""{role:=string(m.Role);if m.Role==sdk.RoleModel{role="assistant"};input=append(input,map[string]any{"type":"message","role":role,"content":text})};case sdk.RoleToolCall:if m.ToolCall!=nil{input=append(input,map[string]any{"type":"function_call","call_id":m.ToolCall.ID,"name":m.ToolCall.Name,"arguments":m.ToolCall.Arguments})};case sdk.RoleToolResult:if m.ToolResult!=nil{input=append(input,map[string]any{"type":"function_call_output","call_id":m.ToolResult.ID,"output":m.ToolResult.Content})}}};b["input"]=input;if len(req.Tools)>0{tools:=make([]any,0,len(req.Tools));for _,t:=range req.Tools{tools=append(tools,map[string]any{"type":"function","name":t.Name,"description":t.Description,"parameters":t.InputSchema})};b["tools"]=tools};if req.Temperature!=nil{b["temperature"]=*req.Temperature};if req.MaxOutputTokens>0{b["max_output_tokens"]=req.MaxOutputTokens};if req.ThinkingLevel!=""&&req.ThinkingLevel!=sdk.ThinkingNone{b["reasoning"]=map[string]any{"effort":string(req.ThinkingLevel)}};return b}
func build(req sdk.Request) map[string]any { return BuildResponsesRequest(req) }
func BuildChatRequest(req sdk.Request) map[string]any {b:=map[string]any{"model":req.Model};messages:=make([]any,0,len(req.Messages)+1);if req.SystemPrompt!=""{messages=append(messages,map[string]any{"role":"system","content":req.SystemPrompt})};for _,m:=range req.Messages{switch m.Role{case sdk.RoleUser,sdk.RoleModel:content:="";for _,p:=range m.Content{content+=p.Text};if content!=""{role:="user";if m.Role==sdk.RoleModel{role="assistant"};messages=append(messages,map[string]any{"role":role,"content":content})};case sdk.RoleToolCall:if m.ToolCall!=nil{messages=append(messages,map[string]any{"role":"assistant","tool_calls":[]any{map[string]any{"id":m.ToolCall.ID,"type":"function","function":map[string]any{"name":m.ToolCall.Name,"arguments":m.ToolCall.Arguments}}}})};case sdk.RoleToolResult:if m.ToolResult!=nil{messages=append(messages,map[string]any{"role":"tool","tool_call_id":m.ToolResult.ID,"content":m.ToolResult.Content})}}};b["messages"]=messages;if len(req.Tools)>0{tools:=make([]any,0,len(req.Tools));for _,t:=range req.Tools{tools=append(tools,map[string]any{"type":"function","function":map[string]any{"name":t.Name,"description":t.Description,"parameters":t.InputSchema}})};b["tools"]=tools};if req.Temperature!=nil{b["temperature"]=*req.Temperature};if req.MaxOutputTokens>0{b["max_tokens"]=req.MaxOutputTokens};return b}
func buildChat(req sdk.Request) map[string]any { return BuildChatRequest(req) }
func ParseResponsesResponse(r response) sdk.Response {out:=sdk.Response{Provider:"openai",Model:r.Model,Usage:sdk.Usage{InputTokens:r.Usage.InputTokens,OutputTokens:r.Usage.OutputTokens,TotalTokens:r.Usage.TotalTokens,CacheReadTokens:r.Usage.InputDetails.Cached},Cache:sdk.CacheInfo{Layer:"provider"}};out.Cache.Hit=out.Usage.CacheReadTokens>0;for _,item:=range r.Output{switch item.Type{case "reasoning":for _,p:=range item.Content{if p.Type=="reasoning_text"&&p.Text!=""{out.Reasoning=&sdk.ReasoningState{ID:item.ID,Text:p.Text};break}};case "message":for _,p:=range item.Content{if p.Text!=""{out.Content=append(out.Content,sdk.ContentPart{Type:sdk.ContentText,Text:p.Text})}};case "function_call":out.ToolCalls=append(out.ToolCalls,sdk.ToolCall{ID:item.CallID,Name:item.Name,Arguments:item.Arguments})}};out.FinishReason=r.Status;return out}
func parseResponse(r response) sdk.Response { return ParseResponsesResponse(r) }
func ParseChatResponse(r chatResponse) sdk.Response {out:=sdk.Response{Provider:"openai",Model:r.Model,Usage:sdk.Usage{InputTokens:r.Usage.PromptTokens,OutputTokens:r.Usage.CompletionTokens,TotalTokens:r.Usage.TotalTokens},Cache:sdk.CacheInfo{Layer:"provider"}};if len(r.Choices)==0{return out};choice:=r.Choices[0];if choice.Message.Content!=""{out.Content=append(out.Content,sdk.ContentPart{Type:sdk.ContentText,Text:choice.Message.Content})};for _,call:=range choice.Message.ToolCalls{out.ToolCalls=append(out.ToolCalls,sdk.ToolCall{ID:call.ID,Name:call.Function.Name,Arguments:call.Function.Arguments})};out.FinishReason=choice.FinishReason;return out}
func parseChatResponse(r chatResponse) sdk.Response { return ParseChatResponse(r) }
func(c *Client)headers()map[string]string{h:=map[string]string{"Authorization":"Bearer "+c.APIKey};for k,v := range c.Headers{if v == "" || strings.EqualFold(k,"Authorization"){continue};h[k]=v};return h}
func cloneHeaders(in map[string]string)map[string]string{if len(in)==0{return nil};out:=make(map[string]string,len(in));for k,v := range in{out[k]=v};return out}

// sdkClient builds an official SDK client bound to this adapter instance.
// Explicit options always win over the SDK's environment defaults
// (OPENAI_API_KEY/OPENAI_BASE_URL), so file-based configuration stays
// authoritative. Retries stay disabled here: the Agent owns retry policy.
func(c *Client) sdkClient(transport http.RoundTripper) oai.Client {
	httpClient := c.http()
	if transport != nil {
		httpClient = &http.Client{Transport: transport}
	}
	opts := []option.RequestOption{option.WithAPIKey(c.APIKey), option.WithMaxRetries(0), option.WithHTTPClient(httpClient)}
	if c.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(c.BaseURL))
	}
	for k, v := range c.Headers {
		if v == "" || strings.EqualFold(k, "Authorization") {
			continue
		}
		opts = append(opts, option.WithHeader(k, v))
	}
	return oai.NewClient(opts...)
}

// roundTripRecorder observes the wire status/headers/body of one SDK call so
// failures can be mapped into the Harness error model without depending on
// the SDK's internal error types.
type roundTripRecorder struct {
	base   http.RoundTripper
	status int
	header http.Header
	body   []byte
}

func newRecorder(base *http.Client) *roundTripRecorder {
	transport := http.DefaultTransport
	if base != nil && base.Transport != nil {
		transport = base.Transport
	}
	return &roundTripRecorder{base: transport}
}

func (t *roundTripRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	t.status = resp.StatusCode
	t.header = resp.Header.Clone()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if readErr == nil {
			t.body = body
			resp.Body = io.NopCloser(bytes.NewReader(body))
			resp.ContentLength = int64(len(body))
		}
	}
	return resp, err
}

// apiError bridges official SDK failures into the Harness error model so
// rate-limit and Retry-After handling keep working unchanged.
type apiError struct {
	status     int
	body       string
	retryAfter time.Duration
	err        error
}

func (e *apiError) Error() string { return e.err.Error() }
func (e *apiError) Unwrap() error { return e.err }
func (e *apiError) HTTPStatusCode() int { return e.status }
func (e *apiError) RetryAfter() time.Duration { return e.retryAfter }

func mapAPIError(err error, rec *roundTripRecorder) error {
	if err == nil {
		return nil
	}
	if rec == nil || rec.status == 0 {
		return err
	}
	mapped := &apiError{status: rec.status, body: strings.TrimSpace(string(rec.body)), err: err}
	if rec.header != nil {
		mapped.retryAfter = parseRetryAfter(rec.header.Get("Retry-After"))
	}
	return mapped
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

func isResponsesModelUnsupported(err error)bool{
	var mapped *apiError
	if !errors.As(err,&mapped){return false}
	if mapped.status==http.StatusNotFound{return true}
	if mapped.status!=http.StatusBadRequest{return false}
	return strings.Contains(mapped.body,`"code":"model_not_supported_on_endpoint"`)&&strings.Contains(mapped.body,`/v1/responses`)
}

func(c *Client)Generate(ctx context.Context,req sdk.Request)(sdk.Response,error){
	rec := newRecorder(c.http())
	client := c.sdkClient(rec)
	resp, err := client.Responses.New(ctx, responsesParams(req))
	if err != nil {
		mapped := mapAPIError(err, rec)
		if !isResponsesModelUnsupported(mapped){return sdk.Response{},mapped}
		return c.generateChat(ctx, req)
	}
	return parseSDKResponse(resp), nil
}

func(c *Client)generateChat(ctx context.Context,req sdk.Request)(sdk.Response,error){
	rec := newRecorder(c.http())
	client := c.sdkClient(rec)
	resp, err := client.Chat.Completions.New(ctx, chatParams(req))
	if err != nil {
		return sdk.Response{}, mapAPIError(err, rec)
	}
	return parseSDKChatResponse(resp), nil
}

func chatParams(req sdk.Request) oai.ChatCompletionNewParams {
	params := oai.ChatCompletionNewParams{Model: shared.ChatModel(req.Model)}
	if req.SystemPrompt != "" {
		params.Messages = append(params.Messages, oai.SystemMessage(req.SystemPrompt))
	}
	for _, m := range req.Messages {
		switch m.Role {
		case sdk.RoleUser, sdk.RoleModel:
			text := ""
			for _, p := range m.Content {
				text += p.Text
			}
			if text == "" {
				continue
			}
			if m.Role == sdk.RoleUser {
				params.Messages = append(params.Messages, oai.UserMessage(text))
			} else {
				params.Messages = append(params.Messages, oai.ChatCompletionMessageParamUnion{OfAssistant: &oai.ChatCompletionAssistantMessageParam{
					Content: oai.ChatCompletionAssistantMessageParamContentUnion{OfString: param.NewOpt(text)},
				}})
			}
		case sdk.RoleToolCall:
			if m.ToolCall == nil {
				continue
			}
			params.Messages = append(params.Messages, oai.ChatCompletionMessageParamUnion{OfAssistant: &oai.ChatCompletionAssistantMessageParam{
				ToolCalls: []oai.ChatCompletionMessageToolCallUnionParam{{OfFunction: &oai.ChatCompletionMessageFunctionToolCallParam{
					ID:       m.ToolCall.ID,
					Function: oai.ChatCompletionMessageFunctionToolCallFunctionParam{Name: m.ToolCall.Name, Arguments: m.ToolCall.Arguments},
				}}},
			}})
		case sdk.RoleToolResult:
			if m.ToolResult != nil {
				params.Messages = append(params.Messages, oai.ToolMessage(m.ToolResult.Content, m.ToolResult.ID))
			}
		}
	}
	for _, t := range req.Tools {
		schema, _ := t.InputSchema.(map[string]any)
		def := shared.FunctionDefinitionParam{Name: t.Name, Parameters: shared.FunctionParameters(schema)}
		if t.Description != "" {
			def.Description = param.NewOpt(t.Description)
		}
		params.Tools = append(params.Tools, oai.ChatCompletionToolUnionParam{OfFunction: &oai.ChatCompletionFunctionToolParam{Function: def}})
	}
	if req.Temperature != nil {
		params.Temperature = param.NewOpt(*req.Temperature)
	}
	if req.MaxOutputTokens > 0 {
		params.MaxTokens = param.NewOpt(int64(req.MaxOutputTokens))
	}
	return params
}

func parseSDKChatResponse(resp *oai.ChatCompletion) sdk.Response {
	var wire chatResponse
	if resp != nil {
		wire.Model = resp.Model
		wire.Usage.PromptTokens = int(resp.Usage.PromptTokens)
		wire.Usage.CompletionTokens = int(resp.Usage.CompletionTokens)
		wire.Usage.TotalTokens = int(resp.Usage.TotalTokens)
		if len(resp.Choices) > 0 {
			choice := resp.Choices[0]
			entry := chatChoice{FinishReason: string(choice.FinishReason)}
			entry.Message.Content = choice.Message.Content
			for _, call := range choice.Message.ToolCalls {
				fn := call.AsFunction()
				entry.Message.ToolCalls = append(entry.Message.ToolCalls, chatToolCall{
					ID:       fn.ID,
					Function: chatFunction{Name: fn.Function.Name, Arguments: fn.Function.Arguments},
				})
			}
			wire.Choices = append(wire.Choices, entry)
		}
	}
	return parseChatResponse(wire)
}

func responsesParams(req sdk.Request) responses.ResponseNewParams {
	params := responses.ResponseNewParams{Model: shared.ResponsesModel(req.Model)}
	if req.SystemPrompt != "" {
		params.Instructions = param.NewOpt(req.SystemPrompt)
	}
	var input []responses.ResponseInputItemUnionParam
	for _, m := range req.Messages {
		switch m.Role {
		case sdk.RoleUser, sdk.RoleModel:
			if m.Reasoning != nil && m.Reasoning.Text != "" {
				summary := []responses.ResponseReasoningItemSummaryParam{{Text: m.Reasoning.Text}}
				input = append(input, responses.ResponseInputItemParamOfReasoning(m.Reasoning.ID, summary))
			}
			text := ""
			for _, p := range m.Content {
				text += p.Text
			}
			if text == "" {
				continue
			}
			role := responses.EasyInputMessageRoleUser
			if m.Role == sdk.RoleModel {
				role = responses.EasyInputMessageRoleAssistant
			}
			input = append(input, responses.ResponseInputItemParamOfMessage(text, role))
		case sdk.RoleToolCall:
			if m.ToolCall != nil {
				input = append(input, responses.ResponseInputItemParamOfFunctionCall(m.ToolCall.Arguments, m.ToolCall.ID, m.ToolCall.Name))
			}
		case sdk.RoleToolResult:
			if m.ToolResult != nil {
				output := responses.ResponseInputItemFunctionCallOutputParam{}
				output.Output.OfString = param.NewOpt(m.ToolResult.Content)
				output.CallID = param.NewOpt(m.ToolResult.ID)
				input = append(input, responses.ResponseInputItemUnionParam{OfFunctionCallOutput: &output})
			}
		}
	}
	if input != nil {
		params.Input.OfInputItemList = input
	}
	for _, t := range req.Tools {
		schema, _ := t.InputSchema.(map[string]any)
		tool := &responses.FunctionToolParam{Name: t.Name, Parameters: schema}
		if t.Description != "" {
			tool.Description = param.NewOpt(t.Description)
		}
		params.Tools = append(params.Tools, responses.ToolUnionParam{OfFunction: tool})
	}
	if req.Temperature != nil {
		params.Temperature = param.NewOpt(*req.Temperature)
	}
	if req.MaxOutputTokens > 0 {
		params.MaxOutputTokens = param.NewOpt(int64(req.MaxOutputTokens))
	}
	if req.ThinkingLevel != "" && req.ThinkingLevel != sdk.ThinkingNone {
		params.Reasoning = shared.ReasoningParam{Effort: shared.ReasoningEffort(req.ThinkingLevel)}
	}
	return params
}

func parseSDKResponse(resp *responses.Response) sdk.Response {
	var wire response
	if resp != nil {
		wire.Model = string(resp.Model)
		wire.Status = string(resp.Status)
		wire.Usage.InputTokens = int(resp.Usage.InputTokens)
		wire.Usage.OutputTokens = int(resp.Usage.OutputTokens)
		wire.Usage.TotalTokens = int(resp.Usage.TotalTokens)
		wire.Usage.InputDetails.Cached = int(resp.Usage.InputTokensDetails.CachedTokens)
		for _, item := range resp.Output {
			switch item.Type {
			case "message":
				for _, part := range item.AsMessage().Content {
					if text := part.AsOutputText(); text.Text != "" {
						wire.Output = append(wire.Output, outputItem{Type: "message", Content: []outputContent{{Type: "output_text", Text: text.Text}}})
					}
				}
			case "reasoning":
				reasoning := item.AsReasoning()
				for _, summary := range reasoning.Summary {
					if summary.Text == "" {
						continue
					}
					wire.Output = append(wire.Output, outputItem{Type: "reasoning", ID: reasoning.ID, Content: []outputContent{{Type: "reasoning_text", Text: summary.Text}}})
					break
				}
			case "function_call":
				call := item.AsFunctionCall()
				wire.Output = append(wire.Output, outputItem{Type: "function_call", CallID: call.CallID, Name: call.Name, Arguments: call.Arguments})
			}
		}
	}
	return parseResponse(wire)
}
func(c *Client)http()*http.Client{if c.HTTP!=nil{return c.HTTP};return http.DefaultClient}
