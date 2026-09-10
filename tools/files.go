package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const maxFileBytes = 4 << 20
const maxSearchResults = 200

func filepathAbsClean(path string) (string, error) { abs, err := filepath.Abs(filepath.Clean(path)); if err != nil { return "", err }; info, err := os.Stat(abs); if err != nil { return "", err }; if !info.IsDir() { return "", errors.New("tools: workspace is not a directory") }; return abs, nil }

func safePath(root, name string) (string, error) {
	if name == "" { name = "." }
	candidate := name; if !filepath.IsAbs(candidate) { candidate = filepath.Join(root, candidate) }
	abs, err := filepath.Abs(filepath.Clean(candidate)); if err != nil { return "", err }
	root, err = filepath.Abs(filepath.Clean(root)); if err != nil { return "", err }
	if !withinRoot(root, abs) { return "", fmt.Errorf("path escapes workspace: %q", name) }
	resolved, err := resolveExistingPath(abs)
	if err != nil { return "", err }
	if !withinRoot(root, resolved) { return "", fmt.Errorf("path escapes workspace through symlink: %q", name) }
	return abs, nil
}

func withinRoot(root, path string) bool { rel, err := filepath.Rel(root, path); return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) }

func resolveExistingPath(path string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(path); err == nil { return filepath.Abs(resolved) }
	current := path
	var missing []string
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current); if err != nil { return "", err }
			resolved, err = filepath.Abs(resolved); if err != nil { return "", err }
			for i := len(missing) - 1; i >= 0; i-- { resolved = filepath.Join(resolved, missing[i]) }
			return resolved, nil
		}
		parent := filepath.Dir(current)
		if parent == current { return "", fmt.Errorf("cannot resolve path: %q", path) }
		missing = append(missing, filepath.Base(current)); current = parent
	}
}

type readFileArgs struct { Path string `json:"path"` }
func readFileTool(root string) handler { return func(_ context.Context, raw json.RawMessage) (string, error) { var args readFileArgs; if err := json.Unmarshal(raw, &args); err != nil { return "", err }; path, err := safePath(root, args.Path); if err != nil { return "", err }; info, err := os.Stat(path); if err != nil { return "", err }; if info.IsDir() { return "", errors.New("path is a directory") }; if info.Size() > maxFileBytes { return "", fmt.Errorf("file exceeds %d byte limit", maxFileBytes) }; data, err := os.ReadFile(path); if err != nil { return "", err }; return string(data), nil } }
type writeFileArgs struct { Path string `json:"path"`; Content string `json:"content"` }
func writeFileTool(root string) handler { return func(_ context.Context, raw json.RawMessage) (string, error) { var args writeFileArgs; if err := json.Unmarshal(raw, &args); err != nil { return "", err }; path, err := safePath(root, args.Path); if err != nil { return "", err }; if int64(len(args.Content)) > maxFileBytes { return "", fmt.Errorf("content exceeds %d byte limit", maxFileBytes) }; if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { return "", err }; if err := os.WriteFile(path, []byte(args.Content), 0644); err != nil { return "", err }; return fmt.Sprintf("wrote %d bytes to %s", len(args.Content), args.Path), nil } }
type editFileArgs struct { Path string `json:"path"`; OldText string `json:"old_text"`; NewText string `json:"new_text"` }
func editFileTool(root string) handler { return func(_ context.Context, raw json.RawMessage) (string, error) { var args editFileArgs; if err := json.Unmarshal(raw, &args); err != nil { return "", err }; if args.OldText == "" { return "", errors.New("old_text must not be empty") }; path, err := safePath(root, args.Path); if err != nil { return "", err }; data, err := os.ReadFile(path); if err != nil { return "", err }; if int64(len(data)) > maxFileBytes { return "", fmt.Errorf("file exceeds %d byte limit", maxFileBytes) }; text := string(data); count := strings.Count(text, args.OldText); if count == 0 { return "", errors.New("old_text was not found") }; if count != 1 { return "", fmt.Errorf("old_text matched %d times; edit_file requires exactly one match", count) }; updated := strings.Replace(text, args.OldText, args.NewText, 1); if int64(len(updated)) > maxFileBytes { return "", fmt.Errorf("edited file exceeds %d byte limit", maxFileBytes) }; if err := os.WriteFile(path, []byte(updated), 0644); err != nil { return "", err }; return fmt.Sprintf("edited %s", args.Path), nil } }
type listDirectoryArgs struct { Path string `json:"path"` }
func listDirectoryTool(root string) handler { return func(_ context.Context, raw json.RawMessage) (string, error) { var args listDirectoryArgs; if err := json.Unmarshal(raw, &args); err != nil { return "", err }; path, err := safePath(root, args.Path); if err != nil { return "", err }; entries, err := os.ReadDir(path); if err != nil { return "", err }; type entry struct { Name string `json:"name"`; Directory bool `json:"directory"` }; out := make([]entry, 0, len(entries)); for _, e := range entries { out = append(out, entry{Name: e.Name(), Directory: e.IsDir()}) }; data, err := json.Marshal(out); return string(data), err } }
type searchFilesArgs struct { Query string `json:"query"`; Path string `json:"path"` }
func searchFilesTool(root string) handler { return func(_ context.Context, raw json.RawMessage) (string, error) { var args searchFilesArgs; if err := json.Unmarshal(raw, &args); err != nil { return "", err }; if args.Query == "" { return "", errors.New("query must not be empty") }; start, err := safePath(root, args.Path); if err != nil { return "", err }; matches := make([]string, 0, 16); err = filepath.WalkDir(start, func(path string, entry fs.DirEntry, walkErr error) error { if walkErr != nil { return walkErr }; if len(matches) >= maxSearchResults { return fs.SkipAll }; if entry.IsDir() { if path != start && entry.Name() == ".git" { return fs.SkipDir }; return nil }; info, err := entry.Info(); if err != nil || info.Size() > maxFileBytes { return nil }; data, err := os.ReadFile(path); if err != nil { return nil }; if strings.Contains(string(data), args.Query) { rel, _ := filepath.Rel(root, path); matches = append(matches, rel) }; return nil }); if err != nil { return "", err }; data, err := json.Marshal(matches); return string(data), err } }
