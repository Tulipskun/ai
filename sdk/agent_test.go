package sdk

import (
	"context"
	"errors"
	"testing"
)

type agentTestProvider struct { responses []Response; calls int }
func (p *agentTestProvider) Name() string { return "test" }
func (p *agentTestProvider) Generate(context.Context, Request) (Response,error) { if p.calls>=len(p.responses){return Response{},errors.New("unexpected call")}; r:=p.responses[p.calls]; p.calls++; return r,nil }
func (p *agentTestProvider) Stream(context.Context, Request) (<-chan Event,error) { return nil,errors.New("not implemented") }
func (p *agentTestProvider) WithAPIKey(string) Provider { return p }

type agentTestTools struct { definitions []Tool; results []ToolResult }
func (t *agentTestTools) Definitions() []Tool { return t.definitions }
func (t *agentTestTools) Execute(_ context.Context, call ToolCall) ToolResult { t.results=append(t.results,ToolResult{ID:call.ID,Content:"ok"}); return t.results[len(t.results)-1] }

func newAgentTestSession(p Provider) (*RouterClient,*Session) {
	r:=NewRouter(); r.RegisterProvider(ProviderConfig{ID:"test",BaseURL:"http://test",Keys:NewKeyPool("key"),Adapter:AdapterOpenAI}); r.Register(ModelRoute{Provider:"test",Model:"model",Adapter:AdapterOpenAI}); c:=NewRouterClient(r); c.RegisterAdapter(AdapterOpenAI,p); s:=NewSession(SessionConfig{ID:"s",Provider:"test",Model:"model",KeyIndex:0},NewKeyPool("key")); return c,s
}

func TestAgentFinalResponse(t *testing.T) {
	p:=&agentTestProvider{responses:[]Response{{Content:[]ContentPart{{Type:ContentText,Text:"done"}}}}}; c,s:=newAgentTestSession(p); a:=&Agent{Client:c,MaxIterations:4}; resp,err:=a.RunTurn(context.Background(),s,Turn{Role:RoleUser,Content:[]ContentPart{{Type:ContentText,Text:"hi"}}},Request{}); if err!=nil{t.Fatal(err)}; if len(resp.Content)!=1||resp.Content[0].Text!="done"{t.Fatalf("unexpected response: %#v",resp)}; if len(s.History())!=2{t.Fatalf("history=%d",len(s.History()))}
}

func TestAgentToolThenFinal(t *testing.T) {
	p:=&agentTestProvider{responses:[]Response{{ToolCalls:[]ToolCall{{ID:"1",Name:"echo",Arguments:"{}"}}},{Content:[]ContentPart{{Type:ContentText,Text:"finished"}}}}}; c,s:=newAgentTestSession(p); tools:=&agentTestTools{definitions:[]Tool{{Name:"echo"}}}; a:=&Agent{Client:c,Tools:tools,MaxIterations:4}; resp,err:=a.RunTurn(context.Background(),s,Turn{Role:RoleUser},Request{}); if err!=nil{t.Fatal(err)}; if resp.Content[0].Text!="finished"{t.Fatalf("unexpected final response")}; h:=s.History(); if len(h)!=4||h[2].Role!=RoleToolResult||h[2].ToolResult.Content!="ok"{t.Fatalf("unexpected history: %#v",h)}; if len(tools.results)!=1{t.Fatalf("tool calls=%d",len(tools.results))}
}

func TestAgentMaxIterations(t *testing.T) {
	p:=&agentTestProvider{responses:[]Response{{ToolCalls:[]ToolCall{{ID:"1",Name:"echo",Arguments:"{}"}}},{ToolCalls:[]ToolCall{{ID:"2",Name:"echo",Arguments:"{}"}}}}}; c,s:=newAgentTestSession(p); a:=&Agent{Client:c,Tools:&agentTestTools{definitions:[]Tool{{Name:"echo"}}},MaxIterations:1}; _,err:=a.RunTurn(context.Background(),s,Turn{Role:RoleUser},Request{}); if !errors.Is(err,ErrAgentMaxIterations){t.Fatalf("err=%v",err)}
}
