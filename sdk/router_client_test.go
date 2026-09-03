package sdk

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeAdapter struct { name string }
func (f *fakeAdapter) Name() string { return f.name }
func (f *fakeAdapter) WithAPIKey(key string) Provider { return &fakeAdapter{name:f.name+":"+key} }
func (f *fakeAdapter) Generate(_ context.Context, req Request) (Response,error) { return Response{Provider:string(req.Provider),Model:req.Model},nil }
func (f *fakeAdapter) Stream(_ context.Context, req Request) (<-chan Event,error) { ch:=make(chan Event,1); ch<-Event{Type:EventDone,Response:&Response{Provider:string(req.Provider),Model:req.Model}};close(ch);return ch,nil }
func (f *fakeAdapter) ListModels(_ context.Context, _ string) ([]Model,error) { return []Model{{ID:"discovered-model"}},nil }

type retryStatusError struct { status int }
func (e retryStatusError) Error() string { return "provider error" }
func (e retryStatusError) HTTPStatusCode() int { return e.status }

type retryingAdapter struct { name string; calls int; err error }
func (a *retryingAdapter) Name() string { return a.name }
func (a *retryingAdapter) WithAPIKey(key string) Provider { return a }
func (a *retryingAdapter) Generate(_ context.Context, req Request) (Response,error) {
	a.calls++
	if a.err != nil { return Response{}, a.err }
	return Response{Provider:string(req.Provider),Model:req.Model},nil
}
func (a *retryingAdapter) Stream(_ context.Context, req Request) (<-chan Event,error) { ch:=make(chan Event,1); ch<-Event{Type:EventDone,Response:&Response{Provider:string(req.Provider),Model:req.Model}};close(ch);return ch,nil }

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

func TestRouterClientRefreshesStaleCatalogue(t *testing.T) {
	r := NewRouter()
	pool := NewKeyPool("key")
	r.RegisterProvider(ProviderConfig{ID: ProviderOpenRouter, Adapter: AdapterOpenAI, Keys: pool})
	c := NewRouterClient(r)
	adapter := &fakeAdapter{name: "openai"}
	c.RegisterAdapter(AdapterOpenAI, adapter)
	session := NewSession(SessionConfig{ID: "catalogue-refresh", Provider: ProviderOpenRouter, Model: "discovered-model"}, pool)
	resp, err := c.Generate(context.Background(), session, Request{})
	if err != nil { t.Fatalf("Generate() error = %v", err) }
	if resp.Model != "discovered-model" { t.Fatalf("model=%q, want discovered-model", resp.Model) }
	if got := r.Models(ProviderOpenRouter); len(got) != 1 || got[0].ID != "discovered-model" { t.Fatalf("catalogue=%v, want discovered-model", got) }
}

func TestRouterClientRetriesHTTP400WithCooldown(t *testing.T) {
	r:=NewRouter()
	r.Register(ModelRoute{Provider:ProviderOpenRouter,Model:"gpt-5",Adapter:AdapterOpenAI})
	adapter:=&retryingAdapter{name:"openai",err:retryStatusError{status:400}}
	c:=NewRouterClient(r)
	c.RegisterAdapter(AdapterOpenAI,adapter)
	c.Retry=RetryPolicy{MaxAttempts:3,InitialBackoff:5*time.Millisecond,MaxBackoff:20*time.Millisecond}
	session:=NewSession(SessionConfig{ID:"retry-400",Provider:ProviderOpenRouter,Model:"gpt-5"},NewKeyPool("key"))
	start:=time.Now()
	_,err:=c.Generate(context.Background(),session,Request{})
	elapsed:=time.Since(start)
	if err==nil { t.Fatal("expected error") }
	if !errors.Is(err,retryStatusError{status:400}) { t.Fatalf("unexpected error: %v",err) }
	if adapter.calls!=3 { t.Fatalf("calls=%d, want 3",adapter.calls) }
	if elapsed < 10*time.Millisecond { t.Fatalf("cooldown too short: %v",elapsed) }
}
