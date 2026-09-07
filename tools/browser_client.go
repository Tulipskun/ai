package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type BrowserClientConfig struct {
	Browser           string
	Profile           string
	Headless          bool
	AllowPrivate      bool
	IdleTimeout       time.Duration
	NavigationTimeout time.Duration
	ActionTimeout     time.Duration
	SnapshotTimeout   time.Duration
}

type BrowserClient struct {
	cfg      BrowserClientConfig
	mu       sync.Mutex
	stateMu  sync.Mutex
	ready    bool
	cmd      *exec.Cmd
	conn     *websocket.Conn
	seq      int64
	sessions map[string]*browserSession
}

type browserSession struct {
	TargetID   string
	CDPSession string
	Refs       map[string]browserRef
	LastUsed   time.Time
}

type browserRef struct {
	Role          string
	Name          string
	BackendNodeID int64
}

func NewBrowserClient(config BrowserClientConfig) *BrowserClient {
	if config.Browser == "" { config.Browser = "auto" }
	if config.IdleTimeout <= 0 { config.IdleTimeout = 30 * time.Minute }
	if config.NavigationTimeout <= 0 { config.NavigationTimeout = 30 * time.Second }
	if config.ActionTimeout <= 0 { config.ActionTimeout = 10 * time.Second }
	if config.SnapshotTimeout <= 0 { config.SnapshotTimeout = 10 * time.Second }
	return &BrowserClient{cfg: config, sessions: make(map[string]*browserSession)}
}

// NewBrowserClientForTest is retained for compatibility with unit tests; browser RPC is now native CDP.
func NewBrowserClientForTest(_ string, _ string, config BrowserClientConfig) *BrowserClient { return NewBrowserClient(config) }

func (c *BrowserClient) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.ready { c.mu.Unlock(); return nil }
	c.mu.Unlock()

	binary, err := findBrowser(c.cfg.Browser)
	if err != nil { return err }
	profile, err := filepath.Abs(c.cfg.Profile)
	if err != nil { return fmt.Errorf("browser profile: %w", err) }
	if err := os.MkdirAll(profile, 0o700); err != nil { return fmt.Errorf("browser: create profile: %w", err) }
	portFile := filepath.Join(profile, "DevToolsActivePort")
	_ = os.Remove(portFile)

	args := []string{
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=0",
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
	}
	if c.cfg.Headless { args = append(args, "--headless=new") }
	if runtime.GOOS == "linux" && os.Geteuid() == 0 { args = append(args, "--no-sandbox") }
	cmd := exec.Command(binary, args...)
	if err := cmd.Start(); err != nil { return fmt.Errorf("start browser %q: %w", binary, err) }

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(portFile); err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) >= 1 {
				port, err := strconv.Atoi(strings.TrimSpace(lines[0]))
				if err == nil && port > 0 {
					wsURL, err := browserWebSocketURL(ctx, port)
					if err == nil {
						conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
						if err == nil {
							c.mu.Lock()
							c.cmd, c.conn, c.ready = cmd, conn, true
							c.mu.Unlock()
							return nil
						}
					}
				}
			}
		}
		if cmd.ProcessState != nil { break }
		time.Sleep(100 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	return fmt.Errorf("browser did not expose a DevTools endpoint")
}

func (c *BrowserClient) Close() error {
	c.mu.Lock()
	conn, cmd := c.conn, c.cmd
	c.conn, c.cmd, c.ready = nil, nil, false
	c.mu.Unlock()
	if conn != nil { _ = conn.Close() }
	if cmd == nil || cmd.Process == nil { return nil }
	if err := cmd.Process.Signal(os.Interrupt); err != nil { _ = cmd.Process.Kill() }
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
	return nil
}

func (c *BrowserClient) Ready() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.ready }

func (c *BrowserClient) Call(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	if !c.ready || c.conn == nil { c.mu.Unlock(); return errors.New("browser is unavailable") }
	c.mu.Unlock()

	switch method {
	case "browser.open":
		var args struct{ SessionID string `json:"session_id"` }
		if err := marshalInto(params, &args); err != nil { return err }
		return c.open(ctx, args.SessionID, result)
	case "browser.close":
		var args struct{ SessionID string `json:"session_id"` }
		if err := marshalInto(params, &args); err != nil { return err }
		return c.closeSession(ctx, args.SessionID, result)
	case "browser.navigate":
		var args struct{ SessionID string `json:"session_id"`; URL string `json:"url"` }
		if err := marshalInto(params, &args); err != nil { return err }
		return c.navigate(ctx, args.SessionID, args.URL, result)
	case "browser.snapshot":
		var args struct{ SessionID string `json:"session_id"` }
		if err := marshalInto(params, &args); err != nil { return err }
		return c.snapshot(ctx, args.SessionID, result)
	case "browser.click", "browser.fill", "browser.press", "browser.select":
		return c.actionRef(ctx, params, strings.TrimPrefix(method, "browser."), result)
	case "browser.scroll":
		return c.scroll(ctx, params, result)
	case "browser.get_text":
		return c.getText(ctx, params, result)
	case "browser.screenshot":
		return c.screenshot(ctx, params, result)
	default:
		return fmt.Errorf("unsupported browser method %q", method)
	}
}

func (c *BrowserClient) open(ctx context.Context, sessionID string, out any) error {
	if strings.TrimSpace(sessionID) == "" { return errors.New("session_id is required") }
	c.stateMu.Lock(); defer c.stateMu.Unlock()
	if s := c.sessions[sessionID]; s != nil {
		meta, _ := c.pageMetadata(ctx, s)
		return jsonInto(out, map[string]any{"session_id":sessionID,"context_id":sessionID,"page_id":s.TargetID,"url":meta.URL,"title":meta.Title})
	}
	var target struct{ TargetID string `json:"targetId"` }
	if err := c.command(ctx, "Target.createTarget", map[string]any{"url":"about:blank"}, "", &target); err != nil { return err }
	var attached struct{ SessionID string `json:"sessionId"` }
	if err := c.command(ctx, "Target.attachToTarget", map[string]any{"targetId":target.TargetID,"flatten":true}, "", &attached); err != nil { return err }
	c.sessions[sessionID] = &browserSession{TargetID:target.TargetID, CDPSession:attached.SessionID, Refs:make(map[string]browserRef), LastUsed:time.Now()}
	return jsonInto(out, map[string]any{"session_id":sessionID,"context_id":sessionID,"page_id":target.TargetID,"url":"about:blank","title":""})
}

func (c *BrowserClient) closeSession(ctx context.Context, sessionID string, out any) error {
	c.stateMu.Lock(); defer c.stateMu.Unlock()
	s := c.sessions[sessionID]
	if s == nil { return jsonInto(out, map[string]any{"closed":false}) }
	_ = c.command(ctx, "Target.closeTarget", map[string]any{"targetId":s.TargetID}, "", nil)
	delete(c.sessions, sessionID)
	return jsonInto(out, map[string]any{"closed":true})
}

func (c *BrowserClient) navigate(ctx context.Context, sessionID, rawURL string, out any) error {
	s, err := c.getSession(sessionID); if err != nil { return err }
	if rawURL == "" { return errors.New("url is required") }
	var nav struct{ ErrorText string `json:"errorText"` }
	if err := c.commandWithTimeout(ctx, "Page.navigate", map[string]any{"url":rawURL}, s.CDPSession, &nav, c.cfg.NavigationTimeout); err != nil { return err }
	if nav.ErrorText != "" { return errors.New(nav.ErrorText) }
	time.Sleep(150 * time.Millisecond)
	meta, _ := c.pageMetadata(ctx, s)
	c.touch(s)
	return jsonInto(out, map[string]any{"page_id":s.TargetID,"url":meta.URL,"title":meta.Title,"status":200})
}

func (c *BrowserClient) snapshot(ctx context.Context, sessionID string, out any) error {
	s, err := c.getSession(sessionID); if err != nil { return err }
	var tree struct{ Nodes []axNode `json:"nodes"` }
	if err := c.commandWithTimeout(ctx, "Accessibility.getFullAXTree", map[string]any{}, s.CDPSession, &tree, c.cfg.SnapshotTimeout); err != nil { return err }
	lines, refs := buildSnapshot(tree.Nodes)
	c.stateMu.Lock(); s.Refs = refs; s.LastUsed = time.Now(); c.stateMu.Unlock()
	meta, _ := c.pageMetadata(ctx, s)
	return jsonInto(out, map[string]any{"page_id":s.TargetID,"url":meta.URL,"title":meta.Title,"snapshot":strings.Join(lines,"\n")})
}

type axNode struct {
	NodeID string `json:"nodeId"`
	BackendDOMNodeID int64 `json:"backendDOMNodeId"`
	Ignored bool `json:"ignored"`
	Role axValue `json:"role"`
	Name axValue `json:"name"`
	ChildIDs []string `json:"childIds"`
	ParentID string `json:"parentId"`
}
type axValue struct { Value any `json:"value"` }

func buildSnapshot(nodes []axNode) ([]string, map[string]browserRef) {
	byID := make(map[string]axNode,len(nodes));children:=make(map[string][]string)
	for _,n:=range nodes{byID[n.NodeID]=n;children[n.ParentID]=append(children[n.ParentID],n.NodeID)}
	rootIDs:=children[""];if len(rootIDs)==0&&len(nodes)>0{rootIDs=[]string{nodes[0].NodeID}}
	refs:=make(map[string]browserRef);var lines []string;counter:=0
	var walk func(string,int);walk=func(id string,depth int){n,ok:=byID[id];if !ok||n.Ignored{return};role:=fmt.Sprint(n.Role.Value);name:=strings.TrimSpace(fmt.Sprint(n.Name.Value));if role=="RootWebArea"{role="page"};line:=strings.Repeat("  ",depth)+"- "+role;if name!=""&&name!="<nil>"{line+=" \""+escapeSnapshotName(name)+"\""};if actionableRole(role)&&n.BackendDOMNodeID>0{counter++;ref:=fmt.Sprintf("e%d",counter);line+=" [ref="+ref+"]";refs[ref]=browserRef{Role:role,Name:name,BackendNodeID:n.BackendDOMNodeID}};if role!="none"&&role!="generic"{lines=append(lines,line)};for _,child:=range n.ChildIDs{walk(child,depth+1)}}
	for _,id:=range rootIDs{walk(id,0)};return lines,refs
}
func actionableRole(role string)bool{switch strings.ToLower(role){case "button","link","textbox","combobox","checkbox","radio","tab","menuitem","option","slider","spinbutton":return true;default:return false}}
func escapeSnapshotName(s string)string{return strings.NewReplacer("\\","\\\\","\"","\\\"").Replace(s)}

func (c *BrowserClient) actionRef(ctx context.Context, params any, action string, out any) error {
	var args struct{SessionID string `json:"session_id"`;Ref string `json:"ref"`;Text string `json:"text"`;Key string `json:"key"`;Value string `json:"value"`}
	if err:=marshalInto(params,&args);err!=nil{return err};s,err:=c.getSession(args.SessionID);if err!=nil{return err};c.stateMu.Lock();descriptor,ok:=s.Refs[args.Ref];c.stateMu.Unlock();if !ok{return fmt.Errorf("stale_reference: reference %s is not valid; take a new browser_snapshot",args.Ref)}
	var object struct{Object struct{ObjectID string `json:"objectId"`} `json:"object`}
	if err:=c.commandWithTimeout(ctx,"DOM.resolveNode",map[string]any{"backendNodeId":descriptor.BackendNodeID},s.CDPSession,&object,c.cfg.ActionTimeout);err!=nil{return err};if object.Object.ObjectID==""{return fmt.Errorf("stale_reference: reference %s no longer resolves",args.Ref)}
	switch action{
	case "click":
		return c.callElement(ctx,s,object.Object.ObjectID,"(el)=>el.click()",nil,out)
	case "fill":
		return c.callElement(ctx,s,object.Object.ObjectID,"(el,v)=>{el.focus();el.value=v;el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));}",[]any{map[string]any{"value":args.Text}},out)
	case "select":
		return c.callElement(ctx,s,object.Object.ObjectID,"(el,v)=>{el.value=v;el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));}",[]any{map[string]any{"value":args.Value}},out)
	case "press":
		if err:=c.callElement(ctx,s,object.Object.ObjectID,"(el)=>el.focus()",nil,nil);err!=nil{return err};key:=args.Key;if key==""{key="Enter"};if err:=c.commandWithTimeout(ctx,"Input.dispatchKeyEvent",map[string]any{"type":"keyDown","key":key},s.CDPSession,c.cfg.ActionTimeout);err!=nil{return err};if err:=c.commandWithTimeout(ctx,"Input.dispatchKeyEvent",map[string]any{"type":"keyUp","key":key},s.CDPSession,c.cfg.ActionTimeout);err!=nil{return err}
		return jsonInto(out,map[string]any{"page_id":s.TargetID,"ok":true})
	default:return fmt.Errorf("unsupported browser action %q",action)
	}
}

func (c *BrowserClient) callElement(ctx context.Context,s *browserSession,objectID,fn string,args []any,out any)error{var value struct{};callArgs:=map[string]any{"objectId":objectID,"functionDeclaration":fn,"returnByValue":true};if args!=nil{callArgs["arguments"]=args};if err:=c.commandWithTimeout(ctx,"Runtime.callFunctionOn",callArgs,s.CDPSession,&value,c.cfg.ActionTimeout);err!=nil{return err};c.touch(s);return jsonInto(out,map[string]any{"page_id":s.TargetID,"ok":true})}

func (c *BrowserClient) scroll(ctx context.Context, params any, out any) error {var args struct{SessionID string `json:"session_id"`;Direction string `json:"direction"`;Amount int `json:"amount"`};if err:=marshalInto(params,&args);err!=nil{return err};if args.Amount<=0{args.Amount=700};s,err:=c.getSession(args.SessionID);if err!=nil{return err};delta:=args.Amount;if strings.EqualFold(args.Direction,"up"){delta=-delta};expr:=fmt.Sprintf("window.scrollBy(0,%d)",delta);if err:=c.commandWithTimeout(ctx,"Runtime.evaluate",map[string]any{"expression":expr,"returnByValue":true},s.CDPSession,&struct{}{},c.cfg.ActionTimeout);err!=nil{return err};c.touch(s);return jsonInto(out,map[string]any{"page_id":s.TargetID,"direction":args.Direction,"amount":args.Amount})}

func (c *BrowserClient) getText(ctx context.Context, params any, out any) error {var args struct{SessionID string `json:"session_id"`;Ref string `json:"ref"`};if err:=marshalInto(params,&args);err!=nil{return err};s,err:=c.getSession(args.SessionID);if err!=nil{return err};if args.Ref==""{var value struct{Result struct{Value string `json:"value"`} `json:"result"`};if err:=c.commandWithTimeout(ctx,"Runtime.evaluate",map[string]any{"expression":"document.body?.innerText || ''","returnByValue":true},s.CDPSession,&value,c.cfg.ActionTimeout);err!=nil{return err};return jsonInto(out,map[string]any{"page_id":s.TargetID,"text":value.Result.Value})};c.stateMu.Lock();descriptor,ok:=s.Refs[args.Ref];c.stateMu.Unlock();if !ok{return fmt.Errorf("stale_reference: reference %s is not valid; take a new browser_snapshot",args.Ref)};var object struct{Object struct{ObjectID string `json:"objectId"`} `json:"object"`};if err:=c.commandWithTimeout(ctx,"DOM.resolveNode",map[string]any{"backendNodeId":descriptor.BackendNodeID},s.CDPSession,&object,c.cfg.ActionTimeout);err!=nil{return err};var value struct{Result struct{Value string `json:"value"`} `json:"result"`};if err:=c.commandWithTimeout(ctx,"Runtime.callFunctionOn",map[string]any{"objectId":object.Object.ObjectID,"functionDeclaration":"(el)=>el.innerText || el.value || ''","returnByValue":true},s.CDPSession,&value,c.cfg.ActionTimeout);err!=nil{return err};return jsonInto(out,map[string]any{"page_id":s.TargetID,"text":value.Result.Value})}

func (c *BrowserClient) screenshot(ctx context.Context, params any, out any) error {var args struct{SessionID string `json:"session_id"`;FullPage bool `json:"full_page"`};if err:=marshalInto(params,&args);err!=nil{return err};s,err:=c.getSession(args.SessionID);if err!=nil{return err};var shot struct{Data string `json:"data"`};if err:=c.commandWithTimeout(ctx,"Page.captureScreenshot",map[string]any{"format":"png","captureBeyondViewport":args.FullPage},s.CDPSession,&shot,c.cfg.ActionTimeout);err!=nil{return err};return jsonInto(out,map[string]any{"page_id":s.TargetID,"content_type":"image/png","base64":shot.Data})}

func (c *BrowserClient) getSession(id string)(*browserSession,error){if strings.TrimSpace(id)==""{return nil,errors.New("session_id is required")};c.stateMu.Lock();defer c.stateMu.Unlock();s:=c.sessions[id];if s==nil{return nil,fmt.Errorf("browser session %s is not open",id)};return s,nil}
func (c *BrowserClient) touch(s *browserSession){c.stateMu.Lock();s.LastUsed=time.Now();c.stateMu.Unlock()}

func (c *BrowserClient) pageMetadata(ctx context.Context,s *browserSession)(struct{URL,Title string},error){var value struct{Result struct{Value struct{URL string `json:"url"`;Title string `json:"title"`} `json:"value"`} `json:"result"`};if err:=c.command(ctx,"Runtime.evaluate",map[string]any{"expression":"({url:location.href,title:document.title})","returnByValue":true},s.CDPSession,&value);err!=nil{return struct{URL,Title string}{},err};return value.Result.Value,nil}
func (c *BrowserClient) command(ctx context.Context,method string,params any,sessionID string,result any)error{return c.commandWithTimeout(ctx,method,params,sessionID,result,c.cfg.ActionTimeout)}
func (c *BrowserClient) commandWithTimeout(ctx context.Context,method string,params any,sessionID string,result any,timeout time.Duration)error{c.mu.Lock();defer c.mu.Unlock();if c.conn==nil{return errors.New("browser is unavailable")};c.seq++;id:=c.seq;message:=map[string]any{"id":id,"method":method,"params":params};if sessionID!=""{message["sessionId"]=sessionID};if err:=c.conn.WriteJSON(message);err!=nil{return err};if timeout<=0{timeout=10*time.Second};_ = c.conn.SetReadDeadline(time.Now().Add(timeout));defer c.conn.SetReadDeadline(time.Time{});for{_,data,err:=c.conn.ReadMessage();if err!=nil{return err};var envelope struct{ID int64 `json:"id"`;Result json.RawMessage `json:"result"`;Error *struct{Code int `json:"code"`;Message string `json:"message"`} `json:"error"`};if err:=json.Unmarshal(data,&envelope);err!=nil{continue};if envelope.ID!=id{continue};if envelope.Error!=nil{return fmt.Errorf("cdp %d: %s",envelope.Error.Code,envelope.Error.Message)};if result!=nil&&len(envelope.Result)>0{return json.Unmarshal(envelope.Result,result)};return nil}}

func browserWebSocketURL(ctx context.Context,port int)(string,error){client:=&http.Client{Timeout:2*time.Second};req,err:=http.NewRequestWithContext(ctx,http.MethodGet,fmt.Sprintf("http://127.0.0.1:%d/json/version",port),nil);if err!=nil{return "",err};resp,err:=client.Do(req);if err!=nil{return "",err};defer resp.Body.Close();var value struct{WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`};if err:=json.NewDecoder(resp.Body).Decode(&value);err!=nil{return "",err};if value.WebSocketDebuggerURL==""{return "",errors.New("browser websocket URL missing")};return value.WebSocketDebuggerURL,nil}

func findBrowser(preference string)(string,error){for _,candidate:=range browserCandidates(preference){if path,err:=exec.LookPath(candidate);err==nil{return path,nil}};for _,path:=range browserAbsoluteCandidates(preference){if _,err:=os.Stat(path);err==nil{return path,nil}};return "",fmt.Errorf("no supported browser installed; looked for Chrome, Chromium, or Edge")}
func browserCandidates(preference string)[]string{switch strings.ToLower(preference){case "chrome":return []string{"google-chrome","google-chrome-stable","chrome"};case "chromium":return []string{"chromium","chromium-browser"};case "edge":return []string{"microsoft-edge","microsoft-edge-stable","msedge"};default:return []string{"google-chrome","google-chrome-stable","chromium","chromium-browser","microsoft-edge","microsoft-edge-stable","msedge"}}
func browserAbsoluteCandidates(preference string)[]string{all:=browserCandidates(preference);if runtime.GOOS=="darwin"{return append(all,"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome","/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge","/Applications/Chromium.app/Contents/MacOS/Chromium")};if runtime.GOOS=="windows"{return append(all,`C:\Program Files\Google\Chrome\Application\chrome.exe`,`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,`C:\Program Files\Microsoft\Edge\Application\msedge.exe`)};return append(all,"/usr/bin/google-chrome","/usr/bin/google-chrome-stable","/usr/bin/chromium","/usr/bin/chromium-browser","/usr/bin/microsoft-edge","/usr/bin/microsoft-edge-stable")}
func marshalInto(value any,out any)error{data,err:=json.Marshal(value);if err!=nil{return err};return json.Unmarshal(data,out)}
func jsonInto(out any,value any)error{if out==nil{return nil};data,err:=json.Marshal(value);if err!=nil{return err};return json.Unmarshal(data,out)}
