package tools

import (
	"context"
	"testing"
)

func TestRegistryDefinitions(t *testing.T) {
	r,err:=NewRegistry(t.TempDir());if err!=nil{t.Fatal(err)}; defs:=r.Definitions(); if len(defs)!=9{t.Fatalf("definitions=%d",len(defs))}
	seen:=map[string]bool{};for _,d:=range defs{seen[d.Name]=true};for _,name:=range []string{"read_file","write_file","edit_file","list_directory","search_files","run_command","run_job","check_job","close_job"}{if !seen[name]{t.Fatalf("missing %s",name)}}
}

func TestRegistryUnknownTool(t *testing.T) { r,_:=NewRegistry(t.TempDir()); res:=r.Execute(context.Background(),sdkCall("missing",map[string]string{}));if !res.IsError{t.Fatal("expected unknown tool error")} }
