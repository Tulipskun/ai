package runtime

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"github.com/Tulipskun/ai/sdk"
)

type CloudflareSessionManager struct {
	config sdk.CloudflareStoreConfig
	base sdk.SessionConfig
	providerKeys map[sdk.ProviderID]*sdk.KeyPool
	mu sync.Mutex
	sessions map[string]*list.Element
	lru *list.List
	maxCached int
}
func NewCloudflareSessionManager(config sdk.CloudflareStoreConfig, base sdk.SessionConfig, providers []sdk.ProviderConfig) *CloudflareSessionManager {
	keys:=make(map[sdk.ProviderID]*sdk.KeyPool,len(providers)); for _,p:=range providers { if p.Keys!=nil { keys[p.ID]=p.Keys } }
	return &CloudflareSessionManager{config:config,base:base,providerKeys:keys,sessions:make(map[string]*list.Element),lru:list.New(),maxCached:4}
}
func (m *CloudflareSessionManager) RegisterProvider(id sdk.ProviderID, keys *sdk.KeyPool) { if m==nil||id==""||keys==nil{return}; m.mu.Lock();m.providerKeys[id]=keys;m.mu.Unlock() }
func (m *CloudflareSessionManager) Resolve(ctx context.Context,input sdk.Input)(*sdk.Session,error){
	if m==nil{return nil,errors.New("runtime: cloudflare session manager is nil")}; if input.SessionID==""{return nil,errors.New("runtime: input SessionID is required")}; if err:=ctx.Err();err!=nil{return nil,err}
	m.mu.Lock(); if e,ok:=m.sessions[input.SessionID];ok {m.lru.MoveToFront(e);s:=e.Value.(*sessionCacheEntry).session;m.mu.Unlock();return s,nil}; cfg:=m.base;cfg.ID=input.SessionID;keys:=m.providerKeys[cfg.Provider];m.mu.Unlock()
	s,err:=sdk.OpenCloudflareSession(m.config,cfg,keys);if err!=nil{return nil,err};loaded:=s.Config();m.mu.Lock();lk:=m.providerKeys[loaded.Provider];m.mu.Unlock();if lk!=nil{if err:=s.SetKeyPool(lk);err!=nil{_ = s.Close();return nil,err}}
	m.mu.Lock();e:=m.lru.PushFront(&sessionCacheEntry{id:input.SessionID,session:s});m.sessions[input.SessionID]=e;for m.lru.Len()>m.maxCached{b:=m.lru.Back();if b==nil{break};entry:=b.Value.(*sessionCacheEntry);delete(m.sessions,entry.id);m.lru.Remove(b)};m.mu.Unlock();return s,nil
}
func (m *CloudflareSessionManager) ListSessions(limit int)([]sdk.SessionInfo,error){return sdk.ListCloudflareSessions(m.config,limit)}
func (m *CloudflareSessionManager) WorkspaceFor(id string)string{m.mu.Lock();defer m.mu.Unlock();if e,ok:=m.sessions[id];ok{return e.Value.(*sessionCacheEntry).session.Config().Workspace};return ""}
func (m *CloudflareSessionManager) Close()error{m.mu.Lock();defer m.mu.Unlock();for id,e:=range m.sessions{_ = e.Value.(*sessionCacheEntry).session.Close();delete(m.sessions,id)};m.lru.Init();return nil}
