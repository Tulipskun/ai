package runtime

import (
	"container/list"
	"context"
	"errors"
	"path/filepath"
	"sync"

	"github.com/Tulipskun/ai/sdk"
)

const defaultMaxCachedSessions = 8

type sessionCacheEntry struct { id string; session *sdk.Session }
type SessionManager struct { dir string; base sdk.SessionConfig; keys *sdk.KeyPool; providerKeys map[sdk.ProviderID]*sdk.KeyPool; mu sync.Mutex; sessions map[string]*list.Element; lru *list.List; maxCached int }

func sessionDir(path string) string {
	clean := filepath.Clean(path)
	if filepath.Ext(clean) == ".db" {
		return filepath.Join(filepath.Dir(clean), "sessions")
	}
	return clean
}

func NewSessionManager(path string,base sdk.SessionConfig,keys *sdk.KeyPool)*SessionManager{return &SessionManager{dir:sessionDir(path),base:base,keys:keys,providerKeys:make(map[sdk.ProviderID]*sdk.KeyPool),sessions:make(map[string]*list.Element),lru:list.New(),maxCached:defaultMaxCachedSessions}}
func NewSessionManagerWithProviders(path string,base sdk.SessionConfig,providers []sdk.ProviderConfig)*SessionManager{providerKeys:=make(map[sdk.ProviderID]*sdk.KeyPool,len(providers));var fallback *sdk.KeyPool;for _,provider:=range providers{if provider.Keys==nil{continue};providerKeys[provider.ID]=provider.Keys;if fallback==nil{fallback=provider.Keys}};if base.Provider!=""&&providerKeys[base.Provider]!=nil{fallback=providerKeys[base.Provider]};return &SessionManager{dir:sessionDir(path),base:base,keys:fallback,providerKeys:providerKeys,sessions:make(map[string]*list.Element),lru:list.New(),maxCached:defaultMaxCachedSessions}}
func(m *SessionManager)RegisterProvider(provider sdk.ProviderID,keys *sdk.KeyPool){if m==nil||provider==""||keys==nil{return};m.mu.Lock();m.providerKeys[provider]=keys;m.mu.Unlock()}
func(m *SessionManager)Resolve(ctx context.Context,input sdk.Input)(*sdk.Session,error){if m==nil{return nil,errors.New("runtime: session manager is nil")};if err:=ctx.Err();err!=nil{return nil,err};if input.SessionID==""{return nil,errors.New("runtime: input SessionID is required")};m.mu.Lock();defer m.mu.Unlock();if elem,ok:=m.sessions[input.SessionID];ok{m.lru.MoveToFront(elem);return elem.Value.(*sessionCacheEntry).session,nil};config:=m.base;config.ID=input.SessionID;keys:=m.keys;if providerKeys:=m.providerKeys[config.Provider];providerKeys!=nil{keys=providerKeys};path:=sdk.SessionDBPath(m.dir,input.SessionID);session,err:=sdk.OpenSession(path,config,keys);if err!=nil{return nil,err};loadedProvider:=session.Config().Provider;if providerKeys:=m.providerKeys[loadedProvider];providerKeys!=nil{if err:=session.SetKeyPool(providerKeys);err!=nil{_=session.Close();return nil,err}};if subProvider:=session.Config().Sub.Provider;subProvider!=""{if providerKeys:=m.providerKeys[subProvider];providerKeys!=nil{if err:=session.SetSubKeyPool(providerKeys);err!=nil{_=session.Close();return nil,err}}};elem:=m.lru.PushFront(&sessionCacheEntry{id:input.SessionID,session:session});m.sessions[input.SessionID]=elem;m.evictLocked();return session,nil}
func(m *SessionManager)evictLocked(){limit:=m.maxCached;if limit<=0{limit=defaultMaxCachedSessions};for m.lru.Len()>limit{elem:=m.lru.Back();if elem==nil{return};entry:=elem.Value.(*sessionCacheEntry);delete(m.sessions,entry.id);m.lru.Remove(elem)}}
func(m *SessionManager)ListSessions(limit int)([]sdk.SessionInfo,error){if m==nil{return nil,errors.New("runtime: session manager is nil")};return sdk.ListSessionsInDir(m.dir,limit)}
func(m *SessionManager)Close()error{if m==nil{return nil};m.mu.Lock();defer m.mu.Unlock();var firstErr error;for id,elem:=range m.sessions{entry:=elem.Value.(*sessionCacheEntry);if err:=entry.session.Close();err!=nil&&firstErr==nil{firstErr=err};delete(m.sessions,id)};m.lru.Init();return firstErr}
