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

func TestReadFilesBatch(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha"), 0644)
	os.WriteFile(filepath.Join(root, "b.txt"), []byte("beta"), 0644)
	r, _ := NewRegistry(root)
	res := r.Execute(context.Background(), sdkCall("read_files", map[string]any{"paths": []string{"a.txt", "b.txt"}}))
	if res.IsError {
		t.Fatal(res.Content)
	}
	var out []struct {
		Path      string `json:"path"`
		Bytes     int64  `json:"bytes"`
		Truncated bool   `json:"truncated"`
		Content   string `json:"content"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Content != "alpha" || out[1].Content != "beta" {
		t.Fatalf("%s", res.Content)
	}
	if out[0].Bytes != 5 || out[0].Truncated || out[0].Error != "" {
		t.Fatalf("%+v", out[0])
	}
}

func TestReadFilesPerFileErrorAndCaps(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "ok.txt"), []byte("0123456789"), 0644)
	r, _ := NewRegistry(root)
	res := r.Execute(context.Background(), sdkCall("read_files", map[string]any{"paths": []string{"ok.txt", "missing.txt", "../outside", "ok.txt"}, "max_bytes": 4, "max_total_bytes": 4}))
	if res.IsError {
		t.Fatal(res.Content)
	}
	var out []struct {
		Path      string `json:"path"`
		Truncated bool   `json:"truncated"`
		Content   string `json:"content"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 {
		t.Fatalf("%s", res.Content)
	}
	if out[0].Content != "0123" || !out[0].Truncated {
		t.Fatalf("%+v", out[0])
	}
	if out[1].Error == "" || out[2].Error == "" {
		t.Fatalf("%+v", out)
	}
	if out[3].Error == "" {
		t.Fatalf("expected total-budget entry: %+v", out[3])
	}
}

func TestReadFilesRejectsEmptyAndTooMany(t *testing.T) {
	r, _ := NewRegistry(t.TempDir())
	if res := r.Execute(context.Background(), sdkCall("read_files", map[string]any{"paths": []string{}})); !res.IsError {
		t.Fatal("expected empty paths to fail")
	}
	many := make([]string, 33)
	for i := range many {
		many[i] = "a.txt"
	}
	if res := r.Execute(context.Background(), sdkCall("read_files", map[string]any{"paths": many})); !res.IsError {
		t.Fatal("expected too many paths to fail")
	}
}
