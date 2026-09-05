package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxJobOutputBytes = 1 << 20

type JobState string
const ( JobRunning JobState = "running"; JobCompleted JobState = "completed"; JobFailed JobState = "failed"; JobClosed JobState = "closed" )
type boundedBuffer struct { mu sync.Mutex; data []byte }
func (b *boundedBuffer) Write(p []byte) (int, error) { b.mu.Lock(); defer b.mu.Unlock(); if len(b.data) < maxJobOutputBytes { n := len(p); if len(b.data)+n > maxJobOutputBytes { n = maxJobOutputBytes-len(b.data) }; b.data = append(b.data,p[:n]...) }; return len(p),nil }
func (b *boundedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return string(append([]byte(nil),b.data...)) }
type job struct { mu sync.RWMutex; id, command string; args []string; cmd *exec.Cmd; state JobState; startedAt, finishedAt time.Time; exitCode int; stdout, stderr boundedBuffer; closed bool; err string }
type JobManager struct { workspace string; statePath string; mu sync.RWMutex; next uint64; jobs map[string]*job; persistMu sync.Mutex }

func NewJobManager(workspace string) *JobManager { m := &JobManager{workspace: workspace, statePath: filepath.Join(workspace, ".ai", "jobs.json"), jobs: make(map[string]*job)}; m.load(); return m }

func (m *JobManager) load() {
	data, err := os.ReadFile(m.statePath); if err != nil { return }
	var records []jobResult; if json.Unmarshal(data,&records) != nil { return }
	for _, r := range records { j := &job{id:r.ID, command:r.Command, args:append([]string(nil),r.Args...), state:r.State, startedAt:r.StartedAt, exitCode:r.ExitCode, err:r.Error}; if r.FinishedAt != nil { j.finishedAt=*r.FinishedAt }; if j.state == JobRunning { j.state=JobFailed; j.err="job manager restarted before the job completed"; j.finishedAt=time.Now(); if j.exitCode == 0 { j.exitCode=-1 } }; m.jobs[j.id]=j; if strings.HasPrefix(j.id,"job-") { if n,err:=strconv.ParseUint(strings.TrimPrefix(j.id,"job-"),10,64); err==nil && n>m.next { m.next=n } } }
}

func (m *JobManager) persist() error {
	m.persistMu.Lock(); defer m.persistMu.Unlock()
	m.mu.RLock(); records:=make([]jobResult,0,len(m.jobs)); for _,j:=range m.jobs { records=append(records,snapshotJob(j)) }; m.mu.RUnlock()
	data,err:=json.MarshalIndent(records,"","  "); if err!=nil{return err}; dir:=filepath.Dir(m.statePath); if err:=os.MkdirAll(dir,0755); err!=nil{return err}; tmp,err:=os.CreateTemp(dir,"jobs-*.json"); if err!=nil{return err}; name:=tmp.Name(); defer os.Remove(name); if _,err:=tmp.Write(data); err!=nil { _=tmp.Close(); return err }; if err:=tmp.Sync(); err!=nil {_=tmp.Close();return err}; if err:=tmp.Close(); err!=nil{return err}; return os.Rename(name,m.statePath)
}

func (m *JobManager) Start(command string,args []string)(string,error){ if command==""{return "",errors.New("command is required")}; cmd:=exec.Command(command,args...); cmd.Dir=m.workspace; j:=&job{command:command,args:append([]string(nil),args...),cmd:cmd,state:JobRunning,startedAt:time.Now(),exitCode:-1}; cmd.Stdout,cmd.Stderr=&j.stdout,&j.stderr; if err:=cmd.Start();err!=nil{return "",err}; m.mu.Lock(); m.next++; j.id=fmt.Sprintf("job-%d",m.next); m.jobs[j.id]=j; m.mu.Unlock(); if err:=m.persist();err!=nil{_ = cmd.Process.Kill(); m.mu.Lock(); delete(m.jobs,j.id); m.mu.Unlock(); return "",fmt.Errorf("persist job: %w",err)}; go m.wait(j); return j.id,nil }
func (m *JobManager) wait(j *job){ err:=j.cmd.Wait(); j.mu.Lock(); j.finishedAt=time.Now(); if j.closed {j.state=JobClosed} else if err!=nil {j.state=JobFailed;j.err=err.Error();if exitErr,ok:=err.(*exec.ExitError);ok{j.exitCode=exitErr.ExitCode()}} else {j.state=JobCompleted;j.exitCode=0}; j.mu.Unlock(); _=m.persist() }
func (m *JobManager) Get(id string)(*job,error){m.mu.RLock();j,ok:=m.jobs[id];m.mu.RUnlock();if !ok{return nil,fmt.Errorf("unknown job: %s",id)};return j,nil}
func (m *JobManager) Close(id string)error{j,err:=m.Get(id);if err!=nil{return err};j.mu.Lock();if j.state!=JobRunning{j.mu.Unlock();return nil};j.closed=true;j.state=JobClosed;process:=j.cmd.Process;j.mu.Unlock();if process==nil{return nil};if err:=process.Kill();err!=nil&&!errors.Is(err,os.ErrProcessDone){return err};_ = m.persist();return nil}

type jobResult struct { ID string `json:"job_id"`; State JobState `json:"state"`; Command string `json:"command"`; Args []string `json:"args,omitempty"`; Output string `json:"output"`; Error string `json:"error,omitempty"`; ExitCode int `json:"exit_code"`; StartedAt time.Time `json:"started_at"`; FinishedAt *time.Time `json:"finished_at,omitempty"` }
func snapshotJob(j *job)jobResult{j.mu.RLock();defer j.mu.RUnlock();r:=jobResult{ID:j.id,State:j.state,Command:j.command,Args:append([]string(nil),j.args...),Output:j.stdout.String(),Error:j.err,ExitCode:j.exitCode,StartedAt:j.startedAt};if !j.finishedAt.IsZero(){v:=j.finishedAt;r.FinishedAt=&v};return r}
func encodeJob(j *job)(string,error){data,err:=json.Marshal(snapshotJob(j));return string(data),err}
func runJobTool(m *JobManager)handler{return func(_ context.Context,raw json.RawMessage)(string,error){var a struct{Command string `json:"command"`;Args []string `json:"args"`};if err:=json.Unmarshal(raw,&a);err!=nil{return "",err};id,err:=m.Start(a.Command,a.Args);if err!=nil{return "",err};data,err:=json.Marshal(map[string]string{"job_id":id});return string(data),err}}
func checkJobTool(m *JobManager)handler{return func(_ context.Context,raw json.RawMessage)(string,error){var a struct{JobID string `json:"job_id"`};if err:=json.Unmarshal(raw,&a);err!=nil{return "",err};j,err:=m.Get(a.JobID);if err!=nil{return "",err};return encodeJob(j)}}
func closeJobTool(m *JobManager)handler{return func(_ context.Context,raw json.RawMessage)(string,error){var a struct{JobID string `json:"job_id"`};if err:=json.Unmarshal(raw,&a);err!=nil{return "",err};if err:=m.Close(a.JobID);err!=nil{return "",err};j,err:=m.Get(a.JobID);if err!=nil{return "",err};return encodeJob(j)}}
