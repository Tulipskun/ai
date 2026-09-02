package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBackgroundJobLifecycle(t *testing.T) {
	root:=t.TempDir(); m:=NewJobManager(root); id,err:=m.Start("sh",[]string{"-c","printf done"});if err!=nil{t.Fatal(err)}
	var state JobState
	for i:=0;i<50;i++{j,_:=m.Get(id); state=snapshotJob(j).State;if state!=JobRunning{break};time.Sleep(10*time.Millisecond)}
	if state!=JobCompleted{t.Fatalf("state=%s",state)}; j,_:=m.Get(id); if !strings.Contains(snapshotJob(j).Output,"done"){t.Fatalf("output=%q",snapshotJob(j).Output)}
}

func TestCloseBackgroundJob(t *testing.T) {
	root:=t.TempDir(); m:=NewJobManager(root); id,err:=m.Start("sh",[]string{"-c","sleep 5"});if err!=nil{t.Fatal(err)}; if err:=m.Close(id);err!=nil{t.Fatal(err)}
	for i:=0;i<50;i++{j,_:=m.Get(id);if snapshotJob(j).State==JobClosed{return};time.Sleep(10*time.Millisecond)}
	j,_:=m.Get(id);t.Fatalf("state=%s",snapshotJob(j).State)
}

func TestRegistryJobTools(t *testing.T) {
	root:=t.TempDir(); r,_:=NewRegistry(root); res:=r.Execute(context.Background(),sdkCall("run_job",map[string]any{"command":"sh","args":[]string{"-c","printf ok"}}));if res.IsError{t.Fatal(res.Content)};var payload map[string]string;if err:=json.Unmarshal([]byte(res.Content),&payload);err!=nil{t.Fatal(err)};if payload["job_id"]==""{t.Fatal("missing job id")}
}
