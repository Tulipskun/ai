package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

func TestFileToolsAndEditSemantics(t *testing.T) {
	root:=t.TempDir(); path:=filepath.Join(root,"main.go"); if err:=os.WriteFile(path,[]byte("one\ntwo\n"),0644);err!=nil{t.Fatal(err)}; r,err:=NewRegistry(root);if err!=nil{t.Fatal(err)}
	res:=r.Execute(context.Background(),sdkCall("edit_file",map[string]string{"path":"main.go","old_text":"two","new_text":"three"}));if res.IsError{t.Fatal(res.Content)};data,_:=os.ReadFile(path);if string(data)!="one\nthree\n"{t.Fatalf("%q",data)}
	res=r.Execute(context.Background(),sdkCall("edit_file",map[string]string{"path":"main.go","old_text":"missing","new_text":"x"}));if !res.IsError{t.Fatal("expected missing edit to fail")}
	res=r.Execute(context.Background(),sdkCall("read_file",map[string]string{"path":"../outside"}));if !res.IsError{t.Fatal("expected traversal rejection")}
}

func TestSearchFiles(t *testing.T) {
	root:=t.TempDir(); os.MkdirAll(filepath.Join(root,"sub"),0755); os.WriteFile(filepath.Join(root,"sub","a.txt"),[]byte("needle"),0644); r,_:=NewRegistry(root); res:=r.Execute(context.Background(),sdkCall("search_files",map[string]string{"query":"needle"}));if res.IsError||!strings.Contains(res.Content,"sub/a.txt"){t.Fatalf("%v %s",res.IsError,res.Content)}
}

func sdkCall(name string, args any) sdk.ToolCall { b,_:=json.Marshal(args); return sdk.ToolCall{ID:"test",Name:name,Arguments:string(b)} }
