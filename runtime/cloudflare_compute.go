package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"github.com/Tulipskun/ai/sdk"
)

type CloudflareJob struct { ID string `json:"id"`; SessionID string `json:"session_id"`; Message string `json:"message"`; Model string `json:"model,omitempty"` }

type CloudflareComputeClient struct { BaseURL string; Token string; Timeout time.Duration; PollInterval time.Duration; Lease time.Duration; HTTP *http.Client }
func NewCloudflareComputeClient(config sdk.CloudflareStoreConfig, poll, lease time.Duration) *CloudflareComputeClient { if config.Timeout<=0{config.Timeout=15*time.Second}; if poll<=0{poll=2*time.Second}; if lease<=0{lease=30*time.Second}; return &CloudflareComputeClient{BaseURL:strings.TrimRight(strings.TrimSpace(config.BaseURL),"/"),Token:strings.TrimSpace(config.Token),Timeout:config.Timeout,PollInterval:poll,Lease:lease,HTTP:&http.Client{Timeout:config.Timeout}} }
func (c *CloudflareComputeClient) Claim(ctx context.Context, workerID string)(*CloudflareJob,error){var out struct{Job *CloudflareJob `json:"job"`};if err:=c.request(ctx,http.MethodGet,"/v1/compute/claim?worker_id="+url.QueryEscape(workerID),nil,&out);err!=nil{return nil,err};return out.Job,nil}
func (c *CloudflareComputeClient) Heartbeat(ctx context.Context,jobID,workerID string)error{return c.request(ctx,http.MethodPost,"/v1/compute/jobs/"+url.PathEscape(jobID)+"/heartbeat",map[string]any{"worker_id":workerID},nil)}
func (c *CloudflareComputeClient) Control(ctx context.Context,jobID string)(bool,error){var out struct{Status string `json:"status"`;CancelRequested bool `json:"cancel_requested"`};if err:=c.request(ctx,http.MethodGet,"/v1/compute/jobs/"+url.PathEscape(jobID)+"/control",nil,&out);err!=nil{return false,err};return out.CancelRequested||out.Status=="cancelled",nil}
func (c *CloudflareComputeClient) Event(ctx context.Context,jobID,eventType string,payload any)error{return c.request(ctx,http.MethodPost,"/v1/compute/jobs/"+url.PathEscape(jobID)+"/events",map[string]any{"type":eventType,"payload":payload,"at_ms":time.Now().UnixMilli()},nil)}
func (c *CloudflareComputeClient) Complete(ctx context.Context,jobID,workerID,status,errText string)error{return c.request(ctx,http.MethodPost,"/v1/compute/jobs/"+url.PathEscape(jobID)+"/complete",map[string]any{"worker_id":workerID,"status":status,"error":errText},nil)}
func (c *CloudflareComputeClient) request(ctx context.Context,method,path string,body any,out any)error{var reader io.Reader;if body!=nil{data,err:=json.Marshal(body);if err!=nil{return err};reader=bytes.NewReader(data)};req,err:=http.NewRequestWithContext(ctx,method,c.BaseURL+path,reader);if err!=nil{return err};req.Header.Set("Authorization","Bearer "+c.Token);if body!=nil{req.Header.Set("Content-Type","application/json")};client:=c.HTTP;if client==nil{client=&http.Client{Timeout:c.Timeout}};resp,err:=client.Do(req);if err!=nil{return err};defer resp.Body.Close();data,err:=io.ReadAll(io.LimitReader(resp.Body,2<<20));if err!=nil{return err};if resp.StatusCode<200||resp.StatusCode>=300{msg:=strings.TrimSpace(string(data));if len(msg)>240{msg=msg[:240]};return fmt.Errorf("runtime: cloudflare compute HTTP %d: %s",resp.StatusCode,msg)};if out==nil||len(data)==0{return nil};return json.Unmarshal(data,out)}

type CloudflareJobDisplay struct { Client *CloudflareComputeClient; JobID string }
func (d *CloudflareJobDisplay) Source() string{return "mobile"}
func (d *CloudflareJobDisplay) Display(ctx context.Context,output sdk.Output)error{if d==nil||d.Client==nil||d.JobID==""{return errors.New("runtime: cloudflare job display is not configured")};if output.Trace!=nil{return d.Client.Event(ctx,d.JobID,"trace",output.Trace)};return d.Client.Event(ctx,d.JobID,"output",map[string]any{"session_id":output.SessionID,"content":output.Content,"response":output.Response,"metadata":output.Metadata})}

type CloudflareComputeWorker struct { Client *CloudflareComputeClient; Sessions *CloudflareSessionManager; Agent *sdk.Agent; BuildRequest sdk.RequestResolver; WorkerID string; currentMu sync.RWMutex; currentJobID string }
func NewCloudflareComputeWorker(client *CloudflareComputeClient,sessions *CloudflareSessionManager,agent *sdk.Agent,buildRequest sdk.RequestResolver,workerID string)*CloudflareComputeWorker{if strings.TrimSpace(workerID)==""{host,_:=os.Hostname();workerID=strings.TrimSpace(host);if workerID==""{workerID="kaggle-worker"}};return &CloudflareComputeWorker{Client:client,Sessions:sessions,Agent:agent,BuildRequest:buildRequest,WorkerID:workerID}}
func(w *CloudflareComputeWorker)setCurrentJob(id string){w.currentMu.Lock();w.currentJobID=id;w.currentMu.Unlock()}
func(w *CloudflareComputeWorker)currentJob()string{w.currentMu.RLock();defer w.currentMu.RUnlock();return w.currentJobID}
func(w *CloudflareComputeWorker)Run(ctx context.Context)error{if w==nil||w.Client==nil||w.Sessions==nil||w.Agent==nil{return errors.New("runtime: incomplete cloudflare compute worker")};w.Agent.SetSubAgentSinks(func(event sdk.SubAgentEvent){id:=w.currentJob();if id!=""{_=w.Client.Event(context.WithoutCancel(ctx),id,"subagent_report",event.Message())}},func(event sdk.SubAgentEvent){id:=w.currentJob();if id!=""&&event.Trace!=nil{_=w.Client.Event(context.WithoutCancel(ctx),id,"subagent_trace",event.Trace)}});for{if err:=ctx.Err();err!=nil{return err};job,err:=w.Client.Claim(ctx,w.WorkerID);if err!=nil{if e:=sleepContext(ctx,w.Client.PollInterval);e!=nil{return e};continue};if job==nil{if e:=sleepContext(ctx,w.Client.PollInterval);e!=nil{return e};continue};if err:=w.runJob(ctx,job);err!=nil&&ctx.Err()!=nil{return ctx.Err()}}}
func(w *CloudflareComputeWorker)runJob(ctx context.Context,job *CloudflareJob)error{w.setCurrentJob(job.ID);defer w.setCurrentJob("");if err:=w.Client.Event(ctx,job.ID,"started",map[string]any{"worker_id":w.WorkerID});err!=nil{return err};jobCtx,cancel:=context.WithCancel(ctx);defer cancel();done:=make(chan struct{});defer close(done);go w.monitorJob(jobCtx,job.ID,done,cancel);loop:=&sdk.HarnessLoop{Agent:w.Agent,ResolveSession:w.Sessions.Resolve,BuildRequest:w.BuildRequest,Displays:[]sdk.Display{&CloudflareJobDisplay{Client:w.Client,JobID:job.ID}},DisplayTimeout:15*time.Second};input:=sdk.Input{Source:"mobile",SessionID:job.SessionID,Turn:sdk.Turn{Role:sdk.RoleUser,Content:[]sdk.ContentPart{{Type:sdk.ContentText,Text:job.Message}}},Metadata:map[string]string{"job_id":job.ID,"model":job.Model}};err:=loop.Entry(jobCtx,input);status,errText:="succeeded","";if err!=nil{if errors.Is(jobCtx.Err(),context.Canceled){status="cancelled"}else{status="failed";errText=err.Error()}};_=w.Client.Event(context.WithoutCancel(ctx),job.ID,"finished",map[string]any{"status":status});return w.Client.Complete(context.WithoutCancel(ctx),job.ID,w.WorkerID,status,errText)}
func(w *CloudflareComputeWorker)monitorJob(ctx context.Context,jobID string,done<-chan struct{},cancel context.CancelFunc){heartbeat:=w.Client.Lease/2;if heartbeat<5*time.Second{heartbeat=5*time.Second};hb:=time.NewTicker(heartbeat);defer hb.Stop();ctrl:=time.NewTicker(2*time.Second);defer ctrl.Stop();for{select{case<-ctx.Done():return;case<-done:return;case<-hb.C:_=w.Client.Heartbeat(context.WithoutCancel(ctx),jobID,w.WorkerID);case<-ctrl.C{cancelled,err:=w.Client.Control(context.WithoutCancel(ctx),jobID);if err==nil&&cancelled{cancel();return}}}}}
func sleepContext(ctx context.Context,d time.Duration)error{if d<=0{d=2*time.Second};t:=time.NewTimer(d);defer t.Stop();select{case<-ctx.Done():return ctx.Err();case<-t.C:return nil}}
