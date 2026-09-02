package sdk

import (
	"context"
	"testing"
)

type fakeAdapter struct { name string }
func (f *fakeAdapter) Name() string { return f.name }
func (f *fakeAdapter) WithAPIKey(key string) Provider { return &fakeAdapter{name:f.name+":"+key} }
func (f *fakeAdapter) Generate(_ context.Context, req Request) (Response,error) { return Response{Provider:string(req.Provider),Model:req.Model},nil }
func (f *fakeAdapter) Stream(_ context.Context, req Request) (<-chan Event,error) { ch:=make(chan Event,1); ch<-Event{Type:EventDone,Response:&Response{Provider:string(req.Provider),Model:req.Model}};close(ch);return ch,nil }

func TestRouterClientDispatchesRequestedRoutes(t *testing.T) {
	r:=NewRouter()
	r.Register(ModelRoute{Provider:ProviderOpenRouter,Model:"gpt-5",Adapter:AdapterOpenAI})
	r.Register(ModelRoute{Provider:ProviderOpenRouter,Model:"gemini-3.5",Adapter:AdapterGemini})
	r.Register(ModelRoute{Provider:ProviderOpenCode,Model:"opus",Adapter:AdapterAnthropic})
	c:=NewRouterClient(r)
	c.RegisterAdapter(AdapterOpenAI,&fakeAdapter{name:"openai"})
	c.RegisterAdapter(AdapterGemini,&fakeAdapter{name:"gemini"})
	c.RegisterAdapter(AdapterAnthropic,&fakeAdapter{name:"anthropic"})
	pool:=NewKeyPool("or-1","or-2","oc-1")
	cases:=[]SessionConfig{{ID:"s1",Provider:ProviderOpenRouter,Model:"gpt-5",KeyIndex:0},{ID:"s2",Provider:ProviderOpenRouter,Model:"gemini-3.5",KeyIndex:1},{ID:"s3",Provider:ProviderOpenCode,Model:"opus",KeyIndex:2}}
	for _,cfg:=range cases { resp,err:=c.Generate(context.Background(),NewSession(cfg,pool),Request{});if err!=nil{t.Fatal(err)};if resp.Provider!=string(cfg.Provider)||resp.Model!=cfg.Model{t.Fatalf("got %+v",resp)} }
}
