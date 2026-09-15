package filestore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const testSession = "discord:1234567890123456789"

func newStore(t *testing.T, limits Limits) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "attachments"), limits)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return store
}

func mustPut(t *testing.T, store *Store, sessionKey, name string, body []byte) Ref {
	t.Helper()
	ref, err := store.Put(context.Background(), sessionKey, name, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Put(%s): %v", name, err)
	}
	return ref
}

func TestPutGetRoundTrip(t *testing.T) {
	store := newStore(t, Limits{MaxFileBytes: 1 << 20, MaxSessionBytes: 4 << 20, TTL: time.Hour})
	body := []byte("attachment payload\nwith two lines")
	ref := mustPut(t, store, testSession, "notes.txt", body)

	if ref.Name != "notes.txt" {
		t.Fatalf("ref name = %q", ref.Name)
	}
	if ref.Size != int64(len(body)) {
		t.Fatalf("ref size = %d, want %d", ref.Size, len(body))
	}
	if ref.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("ref content type = %q", ref.ContentType)
	}
	if !strings.HasPrefix(ref.Path, encodeSessionKey(testSession)+"/files/") {
		t.Fatalf("ref path = %q", ref.Path)
	}

	file, meta, err := store.Get(context.Background(), testSession, ref.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer file.Close()
	got := make([]byte, len(body))
	if _, err := file.Read(got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("round trip mismatch: %q", got)
	}
	if meta.ID != ref.ID || meta.Path != ref.Path || meta.Size != ref.Size {
		t.Fatalf("meta = %+v, ref = %+v", meta, ref)
	}
}

func TestPutStoresOpaqueRefOnly(t *testing.T) {
	store := newStore(t, Limits{MaxFileBytes: 1 << 20, MaxSessionBytes: 4 << 20, TTL: time.Hour})
	secret := "TOP-SECRET-FILE-CONTENT"
	ref := mustPut(t, store, testSession, "secret.txt", []byte(secret))

	encoded, err := os.ReadFile(filepath.Join(store.Root(), ref.Path))
	if err != nil {
		t.Fatalf("stored file missing: %v", err)
	}
	if !bytes.Contains(encoded, []byte(secret)) {
		t.Fatal("stored file should contain the payload")
	}

	// The reference must carry metadata only: no content, base64 or MIME body
	// (REQ-016). Serialising it is the path an agent or transport would take.
	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("marshal ref: %v", err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("ref leaked file content: %s", data)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("unmarshal ref: %v", err)
	}
	for _, unwanted := range []string{"content", "data", "bytes", "base64", "body", "text"} {
		if _, ok := fields[unwanted]; ok {
			t.Fatalf("ref exposes %q field: %s", unwanted, data)
		}
	}
	for _, wanted := range []string{"id", "name", "content_type", "size", "path"} {
		if _, ok := fields[wanted]; !ok {
			t.Fatalf("ref missing %q field: %s", wanted, data)
		}
	}
	if len(fields) != 5 {
		t.Fatalf("ref fields = %d, want 5: %s", len(fields), data)
	}

	metas, err := store.List(context.Background(), testSession)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 1 || metas[0].Name != "secret.txt" {
		t.Fatalf("list = %+v", metas)
	}
	manifestData, err := json.Marshal(metas[0].Ref())
	if err != nil {
		t.Fatalf("marshal meta ref: %v", err)
	}
	if strings.Contains(string(manifestData), secret) {
		t.Fatalf("meta leaked file content: %s", manifestData)
	}
}

func TestPutRejectsFileOverMaxFileBytes(t *testing.T) {
	store := newStore(t, Limits{MaxFileBytes: 8, MaxSessionBytes: 1 << 20, TTL: time.Hour})
	_, err := store.Put(context.Background(), testSession, "big.txt", strings.NewReader("0123456789"))
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("error = %v, want ErrFileTooLarge", err)
	}
	// A rejected upload must leave nothing behind: not a manifest entry, not a
	// temp file, and not even the session directory, because the size checks
	// run before the first filesystem write.
	refs, err := store.List(context.Background(), testSession)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("rejected upload stored: %+v", refs)
	}
	if _, err := os.Lstat(filepath.Join(store.Root(), encodeSessionKey(testSession))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected upload created a session directory: %v", err)
	}
	entries, err := os.ReadDir(store.Root())
	if err != nil {
		t.Fatalf("read store root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("rejected upload left entries: %v", entries)
	}
}

func TestPutRejectsSessionOverMaxSessionBytes(t *testing.T) {
	store := newStore(t, Limits{MaxFileBytes: 16, MaxSessionBytes: 20, TTL: time.Hour})
	first := mustPut(t, store, testSession, "one.bin", bytes.Repeat([]byte("a"), 12))
	if first.Size != 12 {
		t.Fatalf("first size = %d", first.Size)
	}
	_, err := store.Put(context.Background(), testSession, "two.bin", strings.NewReader("0123456789ab"))
	if !errors.Is(err, ErrSessionTooLarge) {
		t.Fatalf("error = %v, want ErrSessionTooLarge", err)
	}
	// The session keeps the accepted attachment only.
	refs, err := store.List(context.Background(), testSession)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(refs) != 1 || refs[0].ID != first.ID {
		t.Fatalf("session attachments = %+v", refs)
	}
	// The remaining budget is still usable, so an exact-size upload fits.
	if _, err := store.Put(context.Background(), testSession, "two.bin", strings.NewReader("01234567")); err != nil {
		t.Fatalf("upload inside remaining budget: %v", err)
	}
	// A full session rejects further uploads outright.
	if _, err := store.Put(context.Background(), testSession, "three.bin", strings.NewReader("x")); !errors.Is(err, ErrSessionTooLarge) {
		t.Fatalf("full session error = %v, want ErrSessionTooLarge", err)
	}
	refs, err = store.List(context.Background(), testSession)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("session attachments = %+v", refs)
	}
}

func TestSessionBudgetsAreIsolated(t *testing.T) {
	store := newStore(t, Limits{MaxFileBytes: 16, MaxSessionBytes: 16, TTL: time.Hour})
	mustPut(t, store, "session-a", "a.txt", []byte("0123456789abcdef"))
	if _, err := store.Put(context.Background(), "session-b", "b.txt", strings.NewReader("0123456789abcdef")); err != nil {
		t.Fatalf("second session should have its own budget: %v", err)
	}
	refs, err := store.List(context.Background(), "session-b")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("session-b attachments = %+v", refs)
	}
}

func TestGetRejectsTraversalAndForeignSessions(t *testing.T) {
	store := newStore(t, Limits{MaxFileBytes: 1 << 20, MaxSessionBytes: 4 << 20, TTL: time.Hour})
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("do not read me"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	ref := mustPut(t, store, "session-a", "ok.txt", []byte("inside"))

	for _, id := range []string{
		"../../../../etc/passwd",
		".." + string(filepath.Separator) + ".." + string(filepath.Separator) + "outside.txt",
		filepath.ToSlash(outside),
		"",
		"no-such-file",
		"../../session-a/files/" + ref.ID,
	} {
		if _, _, err := store.Get(context.Background(), "session-a", id); err == nil {
			t.Fatalf("Get(%q) succeeded, want rejection", id)
		} else if !errors.Is(err, ErrInvalidRef) && !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get(%q) error = %v", id, err)
		}
	}

	// A reference from another session must not resolve.
	if _, _, err := store.Get(context.Background(), "session-b", ref.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-session Get error = %v, want ErrNotFound", err)
	}
	// An unknown reference id is also not found.
	unknown := strings.Repeat("A", 22)
	if _, _, err := store.Get(context.Background(), "session-a", unknown); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown ref error = %v, want ErrNotFound", err)
	}
}

func TestGetRejectsSymlinkEscape(t *testing.T) {
	if os.Geteuid() == 0 && false {
		t.Skip("no symlinks")
	}
	store := newStore(t, Limits{MaxFileBytes: 1 << 20, MaxSessionBytes: 4 << 20, TTL: time.Hour})
	ref := mustPut(t, store, "session-a", "victim.txt", []byte("original"))
	sessionDir := filepath.Join(store.Root(), encodeSessionKey("session-a"))
	target := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(target, []byte("outside secret"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(sessionDir, "files", ref.ID)
	if err := os.Remove(link); err != nil {
		t.Fatalf("remove stored file: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink setup: %v", err)
	}
	file, _, err := store.Get(context.Background(), "session-a", ref.ID)
	if err == nil {
		_ = file.Close()
		t.Fatal("Get followed a symlink outside the store root")
	}
	if !strings.Contains(err.Error(), "escape") && !strings.Contains(err.Error(), "invalid attachment reference") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestSessionKeyIsSanitizedForPath(t *testing.T) {
	store := newStore(t, Limits{MaxFileBytes: 1 << 20, MaxSessionBytes: 4 << 20, TTL: time.Hour})
	hostile := "../../escape/../session:with spaces#and?symbols"
	ref := mustPut(t, store, hostile, "note.txt", []byte("payload"))

	if strings.Contains(ref.Path, "..") {
		t.Fatalf("session key leaked traversal into path: %q", ref.Path)
	}
	if strings.ContainsAny(ref.Path, " ?#\\") {
		t.Fatalf("session key not sanitized: %q", ref.Path)
	}
	first := strings.Split(ref.Path, "/")[0]
	if first == encodeSessionKey("x") || first == hostile {
		t.Fatalf("unexpected directory name %q", first)
	}
	if !validSessionKeyDir(first) {
		t.Fatalf("directory %q is not a base64url session key", first)
	}
	if _, err := os.Lstat(filepath.Join(store.Root(), "..", "escape")); err == nil {
		t.Fatal("store wrote outside its root")
	}
	entries, err := os.ReadDir(store.Root())
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("store root entries = %v", entries)
	}
	// The same raw key resolves to the same directory, so continuation works.
	if _, _, err := store.Get(context.Background(), hostile, ref.ID); err != nil {
		t.Fatalf("Get with the same raw key: %v", err)
	}
	// And an empty key is refused outright.
	if _, err := store.Put(context.Background(), "   ", "x.txt", strings.NewReader("y")); !errors.Is(err, ErrEmptySessionKey) {
		t.Fatalf("empty session key error = %v", err)
	}
}

func TestManifestSurvivesReopenAndTracksDeletes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "attachments")
	limits := Limits{MaxFileBytes: 1 << 20, MaxSessionBytes: 4 << 20, TTL: time.Hour}
	store, err := Open(root, limits)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	one := mustPut(t, store, testSession, "one.txt", []byte("first"))
	two := mustPut(t, store, testSession, "two.txt", []byte("second"))

	manifestPath := filepath.Join(root, encodeSessionKey(testSession), "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("manifest missing after Put: %v", err)
	}
	if strings.Contains(string(raw), "first") || strings.Contains(string(raw), "second") {
		t.Fatalf("manifest holds file content: %s", raw)
	}

	// Reopening the same root must see the manifest again.
	reopened, err := Open(root, limits)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	manifest, err := reopened.Manifest(context.Background(), testSession)
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	if manifest.Version != manifestVersion || len(manifest.Files) != 2 {
		t.Fatalf("manifest = %+v", manifest)
	}
	if got := manifest.usedBytes(); got != one.Size+two.Size {
		t.Fatalf("used bytes = %d, want %d", got, one.Size+two.Size)
	}
	if _, _, err := reopened.Get(context.Background(), testSession, two.ID); err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}

	if err := reopened.Delete(context.Background(), testSession, one.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	manifest, err = reopened.Manifest(context.Background(), testSession)
	if err != nil {
		t.Fatalf("Manifest after delete: %v", err)
	}
	if len(manifest.Files) != 1 || manifest.Files[0].ID != two.ID {
		t.Fatalf("manifest after delete = %+v", manifest.Files)
	}
	if _, err := os.Lstat(filepath.Join(root, encodeSessionKey(testSession), "files", one.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted file still present: %v", err)
	}
	if err := reopened.Delete(context.Background(), testSession, one.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete error = %v", err)
	}
	// Deleting the last attachment removes the session directory entirely.
	if err := reopened.Delete(context.Background(), testSession, two.ID); err != nil {
		t.Fatalf("Delete last: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, encodeSessionKey(testSession))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session directory left behind: %v", err)
	}
	empty, err := reopened.List(context.Background(), testSession)
	if err != nil {
		t.Fatalf("List after wipe: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("list after wipe = %+v", empty)
	}
}

func TestCleanupRemovesExpiredAttachments(t *testing.T) {
	store := newStore(t, Limits{MaxFileBytes: 1 << 20, MaxSessionBytes: 4 << 20, TTL: time.Hour})
	now := time.Now().UTC()
	expired := mustPut(t, store, "session-a", "old.txt", []byte("old payload"))
	keep := mustPut(t, store, "session-a", "new.txt", []byte("new payload"))
	other := mustPut(t, store, "session-b", "other.txt", []byte("other payload"))

	// Backdate one attachment by rewriting its manifest timestamp.
	if err := backdateManifest(filepath.Join(store.Root(), encodeSessionKey("session-a"), "manifest.json"), expired.ID, now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	// A directory that is not a session key must be ignored, not deleted.
	foreign := filepath.Join(store.Root(), "not-base64url!!")
	if err := os.MkdirAll(foreign, 0o700); err != nil {
		t.Fatalf("mkdir foreign: %v", err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "keep.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write foreign: %v", err)
	}

	removed, err := store.Cleanup(context.Background(), now)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, _, err := store.Get(context.Background(), "session-a", expired.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired attachment still readable: %v", err)
	}
	if _, _, err := store.Get(context.Background(), "session-a", keep.ID); err != nil {
		t.Fatalf("fresh attachment removed: %v", err)
	}
	if _, _, err := store.Get(context.Background(), "session-b", other.ID); err != nil {
		t.Fatalf("other session attachment removed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(foreign, "keep.txt")); err != nil {
		t.Fatalf("Cleanup touched a non-session directory: %v", err)
	}
	// The manifest is still there for the surviving session.
	if _, err := os.Lstat(filepath.Join(store.Root(), encodeSessionKey("session-a"), "manifest.json")); err != nil {
		t.Fatalf("manifest removed by cleanup: %v", err)
	}

	// Everything expires later; the emptied session directory is dropped.
	removed, err = store.Cleanup(context.Background(), now.Add(30*24*time.Hour))
	if err != nil {
		t.Fatalf("Cleanup all: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	for _, key := range []string{"session-a", "session-b"} {
		if _, err := os.Lstat(filepath.Join(store.Root(), encodeSessionKey(key))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s directory still present: %v", key, err)
		}
	}
}

func TestCleanupSweepsUnrecordedTempFiles(t *testing.T) {
	store := newStore(t, Limits{MaxFileBytes: 1 << 20, MaxSessionBytes: 4 << 20, TTL: time.Hour})
	keep := mustPut(t, store, "session-a", "a.txt", []byte("payload"))
	filesDir := filepath.Join(store.Root(), encodeSessionKey("session-a"), "files")
	stale := filepath.Join(filesDir, tmpPrefix+"crashed-upload")
	if err := os.WriteFile(stale, []byte("partial"), 0o600); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	old := time.Now().UTC().Add(-48 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	removed, err := store.Cleanup(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Lstat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale temp file survived cleanup: %v", err)
	}
	if _, _, err := store.Get(context.Background(), "session-a", keep.ID); err != nil {
		t.Fatalf("recorded attachment removed: %v", err)
	}
}

func TestOpenRejectsBadLimits(t *testing.T) {
	root := filepath.Join(t.TempDir(), "attachments")
	if _, err := Open(root, Limits{MaxFileBytes: -1}); err == nil {
		t.Fatal("negative max file size accepted")
	}
	if _, err := Open(root, Limits{TTL: -time.Second}); err == nil {
		t.Fatal("negative ttl accepted")
	}
	if _, err := Open(root, Limits{MaxFileBytes: 100, MaxSessionBytes: 10}); err == nil {
		t.Fatal("session limit below file limit accepted")
	}
	if _, err := Open("", Limits{}); err == nil {
		t.Fatal("empty root accepted")
	}
	if _, err := Open(string(filepath.Separator), Limits{}); err == nil {
		t.Fatal("filesystem root accepted")
	}
	store, err := Open(root, Limits{})
	if err != nil {
		t.Fatalf("Open with zero limits: %v", err)
	}
	if store.Limits() != DefaultLimits() {
		t.Fatalf("limits = %+v", store.Limits())
	}
}

// TestOpenUnderRejectsEscapingRootWithoutWriting pins the ordering: a root that
// escapes the state root must be rejected by validation alone, leaving no
// directory or file behind either inside or outside the state root.
func TestOpenUnderRejectsEscapingRootWithoutWriting(t *testing.T) {
	parent := t.TempDir()
	state := filepath.Join(parent, "state")
	if err := os.MkdirAll(filepath.Join(state, "data"), 0o700); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	before := dirNames(t, state)

	for name, tc := range map[string]struct {
		dir    string
		target string
	}{
		"relative traversal": {
			dir:    ".." + string(filepath.Separator) + "escaped",
			target: filepath.Join(parent, "escaped"),
		},
		"deep traversal": {
			dir:    filepath.Join("data", "..", "..", "escaped-deep"),
			target: filepath.Join(parent, "escaped-deep"),
		},
		"absolute outside": {
			dir:    filepath.Join(parent, "abs-escaped"),
			target: filepath.Join(parent, "abs-escaped"),
		},
		"state root itself": {
			dir: ".",
			// The state root pre-exists, so the assertion is that no store
			// child appears inside it.
			target: filepath.Join(state, DefaultRoot),
		},
		"session directory": {
			dir:    filepath.Join("data", "sessions", "attachments"),
			target: filepath.Join(state, "data", "sessions", "attachments"),
		},
		"shared data directory": {
			dir:    "data",
			target: filepath.Join(state, "data", DefaultRoot),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := OpenUnder(state, tc.dir, Limits{}); err == nil {
				t.Fatalf("OpenUnder(%q) accepted", tc.dir)
			}
			if _, err := os.Lstat(tc.target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected root %q still created %s: %v", tc.dir, tc.target, err)
			}
			if got := dirNames(t, state); !equalNames(before, got) {
				t.Fatalf("state root changed after rejection: before=%v after=%v", before, got)
			}
			if _, err := os.Lstat(filepath.Join(parent, "escaped")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("wrote outside the state root: %v", err)
			}
		})
	}
}

// TestOpenUnderRejectsSymlinkedAncestorWithoutWriting covers the escape route
// where a *parent* of the store root is a symlink: MkdirAll would have created
// the whole chain through the link before the old post-create check ran.
func TestOpenUnderRejectsSymlinkedAncestorWithoutWriting(t *testing.T) {
	parent := t.TempDir()
	state := filepath.Join(parent, "state")
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(state, "data")); err != nil {
		t.Fatalf("symlink setup: %v", err)
	}
	if _, err := OpenUnder(state, filepath.Join("data", "attachments"), Limits{}); err == nil {
		t.Fatal("store root under a symlinked data directory was accepted")
	}
	if _, err := os.Lstat(filepath.Join(outside, "attachments")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected root created %s: %v", filepath.Join(outside, "attachments"), err)
	}
	if entries := dirNames(t, outside); len(entries) != 0 {
		t.Fatalf("rejected root wrote into the outside directory: %v", entries)
	}
}

// TestOpenRejectsSymlinkedRootWithoutCreating checks the bare Open path: a
// store root that is itself a symlink out of the way must be refused before any
// directory is created.
func TestOpenRejectsSymlinkedRootWithoutCreating(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink setup: %v", err)
	}
	if _, err := Open(link, Limits{}); err == nil {
		t.Fatal("symlinked store root accepted")
	}
	if entries := dirNames(t, target); len(entries) != 0 {
		t.Fatalf("rejected Open wrote into the symlink target: %v", entries)
	}
}

// TestOpenUnderEmptyDirIsDefault documents the decided contract: an empty dir
// is the intentional default and resolves to DefaultRoot, while a missing state
// root is an error.
func TestOpenUnderEmptyDirIsDefault(t *testing.T) {
	state := t.TempDir()
	for _, dir := range []string{"", "   ", "\t\n"} {
		store, err := OpenUnder(state, dir, Limits{})
		if err != nil {
			t.Fatalf("OpenUnder(%q): %v", dir, err)
		}
		if store.Root() != filepath.Join(state, DefaultRoot) {
			t.Fatalf("root for %q = %q, want %q", dir, store.Root(), filepath.Join(state, DefaultRoot))
		}
	}
	if _, err := OpenUnder("", "data/attachments", Limits{}); err == nil {
		t.Fatal("empty state root accepted")
	}
	if _, err := OpenUnder(state, string(filepath.Separator), Limits{}); err == nil {
		t.Fatal("filesystem root accepted")
	}
}

func TestOpenUnderKeepsStoreInsideStateRoot(t *testing.T) {
	state := t.TempDir()
	store, err := OpenUnder(state, "", Limits{})
	if err != nil {
		t.Fatalf("OpenUnder default: %v", err)
	}
	if store.Root() != filepath.Join(state, DefaultRoot) {
		t.Fatalf("root = %q, want %q", store.Root(), filepath.Join(state, DefaultRoot))
	}
	info, err := os.Stat(store.Root())
	if err != nil {
		t.Fatalf("store directory not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("store permissions = %o, want 700", perm)
	}

	cases := map[string]string{
		"escapes the state root":        ".." + string(filepath.Separator) + "outside",
		"is the state root itself":      ".",
		"is the shared data directory":  "data",
		"sits in the session directory": filepath.Join("data", "sessions", "attachments"),
		"is the session directory":      filepath.Join("data", "sessions"),
	}
	for name, root := range cases {
		if _, err := OpenUnder(state, root, Limits{}); err == nil {
			t.Fatalf("OpenUnder(%q) accepted: %s", root, name)
		}
	}
	// An absolute root inside the state root is fine.
	if _, err := OpenUnder(state, filepath.Join(state, "data", "attachments"), Limits{}); err != nil {
		t.Fatalf("absolute in-state root: %v", err)
	}
}

func TestOpenUnderRejectsSymlinkedEscape(t *testing.T) {
	state := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(state, "attachments")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink setup: %v", err)
	}
	if _, err := OpenUnder(state, "attachments", Limits{}); err == nil {
		t.Fatal("symlinked attachment root escaping the state root was accepted")
	}
}

func TestPutRejectsContextCancellation(t *testing.T) {
	store := newStore(t, Limits{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Put(ctx, testSession, "x.txt", strings.NewReader("data")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Put error = %v", err)
	}
	if _, _, err := store.Get(ctx, testSession, "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get error = %v", err)
	}
	if _, err := store.List(ctx, testSession); !errors.Is(err, context.Canceled) {
		t.Fatalf("List error = %v", err)
	}
	if _, err := store.Cleanup(ctx, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Cleanup error = %v", err)
	}
}

func TestPutWithContentTypeOverride(t *testing.T) {
	store := newStore(t, Limits{})
	ref, err := store.PutWithContentType(context.Background(), testSession, "report", "application/pdf", strings.NewReader("plain bytes"))
	if err != nil {
		t.Fatalf("PutWithContentType: %v", err)
	}
	if ref.ContentType != "application/pdf" {
		t.Fatalf("content type = %q", ref.ContentType)
	}
	if ref.Name != "report" {
		t.Fatalf("name = %q", ref.Name)
	}
}

func TestSanitizeNameIsDisplayOnly(t *testing.T) {
	for name, want := range map[string]string{
		"../../etc/passwd":       "passwd",
		"report final.pdf":       "report final.pdf",
		"..":                     fallbackName,
		"":                       fallbackName,
		"weird\x00name":          "weird_name",
		"C:\\temp\\a.png":        "a.png",
		strings.Repeat("x", 400): strings.Repeat("x", maxNameLen),
	} {
		if got := sanitizeName(name); got != want {
			t.Fatalf("sanitizeName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestValidFileIDAndSessionKeyDir(t *testing.T) {
	if !validFileID(encodeFileIDForTest(t)) {
		t.Fatal("generated id rejected")
	}
	for _, bad := range []string{"", "..", "../../x", "short", strings.Repeat("A", 21), strings.Repeat("A", 23), "has/slash", "with space", "a+b/c=d"} {
		if validFileID(bad) {
			t.Fatalf("validFileID(%q) = true", bad)
		}
	}
	if !validSessionKeyDir(encodeSessionKey("some session")) {
		t.Fatal("encoded session key rejected")
	}
	for _, bad := range []string{"", "not-base64url!!", "..", "a=="} {
		if validSessionKeyDir(bad) {
			t.Fatalf("validSessionKeyDir(%q) = true", bad)
		}
	}
}

// dirNames lists the sorted entry names of a directory so a test can assert
// that a rejected Open left the directory exactly as it found it.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		t.Fatalf("read dir %q: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func equalNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestManifestLivesUnderStoreRootOnly(t *testing.T) {
	state := t.TempDir()
	store, err := OpenUnder(state, "", Limits{MaxFileBytes: 1 << 20, MaxSessionBytes: 4 << 20, TTL: time.Hour})
	if err != nil {
		t.Fatalf("OpenUnder: %v", err)
	}
	ref := mustPut(t, store, testSession, "notes.txt", []byte("manifest payload"))

	wantSessionDir := filepath.Join(store.Root(), encodeSessionKey(testSession))
	manifestPath := filepath.Join(wantSessionDir, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("manifest not under the store root: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if decoded["version"] != float64(manifestVersion) {
		t.Fatalf("manifest version = %v", decoded["version"])
	}
	files, ok := decoded["files"].([]any)
	if !ok || len(files) != 1 {
		t.Fatalf("manifest files = %v", decoded["files"])
	}
	entry := files[0].(map[string]any)
	for _, wanted := range []string{"id", "name", "content_type", "size", "path", "created_at"} {
		if _, ok := entry[wanted]; !ok {
			t.Fatalf("manifest entry missing %q: %s", wanted, raw)
		}
	}
	for _, unwanted := range []string{"content", "data", "bytes", "base64", "body", "text"} {
		if _, ok := entry[unwanted]; ok {
			t.Fatalf("manifest entry exposes %q: %s", unwanted, raw)
		}
	}
	if entry["path"] != filepath.ToSlash(filepath.Join(encodeSessionKey(testSession), "files", ref.ID)) {
		t.Fatalf("manifest path = %v, ref path = %q", entry["path"], ref.Path)
	}
	// The payload stays in exactly one file, and the session databases are
	// untouched (CON-002, CON-003, CON-011).
	entries, err := os.ReadDir(filepath.Join(wantSessionDir, "files"))
	if err != nil {
		t.Fatalf("read files dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != ref.ID {
		t.Fatalf("stored files = %v", entries)
	}
	if _, err := os.Stat(filepath.Join(state, "data", "sessions")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("attachment store touched the session directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(state, "data", "attachments")); err != nil {
		t.Fatalf("default store root missing: %v", err)
	}
}

func TestPutStoresEmptyAttachment(t *testing.T) {
	store := newStore(t, Limits{MaxFileBytes: 16, MaxSessionBytes: 32, TTL: time.Hour})
	ref := mustPut(t, store, testSession, "empty.txt", nil)
	if ref.Size != 0 {
		t.Fatalf("size = %d, want 0", ref.Size)
	}
	file, meta, err := store.Get(context.Background(), testSession, ref.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer file.Close()
	if meta.Size != 0 {
		t.Fatalf("meta size = %d", meta.Size)
	}
}

func TestPutRejectsNilReader(t *testing.T) {
	store := newStore(t, Limits{})
	if _, err := store.Put(context.Background(), testSession, "x.txt", nil); err == nil {
		t.Fatal("nil reader accepted")
	}
	// Rejection must not create a session directory either.
	if _, err := os.Lstat(filepath.Join(store.Root(), encodeSessionKey(testSession))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nil reader created a session directory: %v", err)
	}
}

func TestListOrdersOldestFirst(t *testing.T) {
	store := newStore(t, Limits{MaxFileBytes: 1 << 20, MaxSessionBytes: 4 << 20, TTL: time.Hour})
	first := mustPut(t, store, testSession, "one.txt", []byte("1"))
	second := mustPut(t, store, testSession, "two.txt", []byte("22"))
	third := mustPut(t, store, testSession, "three.txt", []byte("333"))

	metas, err := store.List(context.Background(), testSession)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{first.ID, second.ID, third.ID}
	for i, id := range want {
		if metas[i].ID != id {
			t.Fatalf("list order = %v, want %v", []string{metas[0].ID, metas[1].ID, metas[2].ID}, want)
		}
	}
	// A reference view of every listed attachment is opaque metadata only.
	for _, meta := range metas {
		ref := meta.Ref()
		if ref.ID != meta.ID || ref.Path != meta.Path || ref.Size != meta.Size || ref.Name != meta.Name || ref.ContentType != meta.ContentType {
			t.Fatalf("Ref() = %+v, Meta = %+v", ref, meta)
		}
	}
	// List of a session that never stored anything is empty, not an error.
	empty, err := store.List(context.Background(), "unused-session")
	if err != nil {
		t.Fatalf("List unused: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("unused session = %+v", empty)
	}
}

func TestDeleteValidatesRefAndSession(t *testing.T) {
	store := newStore(t, Limits{MaxFileBytes: 1 << 20, MaxSessionBytes: 4 << 20, TTL: time.Hour})
	victim := mustPut(t, store, "session-a", "a.txt", []byte("payload"))

	for _, bad := range []string{"", "..", "../../session-a/files/" + victim.ID, "short"} {
		if err := store.Delete(context.Background(), "session-a", bad); !errors.Is(err, ErrInvalidRef) {
			t.Fatalf("Delete(%q) error = %v, want ErrInvalidRef", bad, err)
		}
	}
	// Another session cannot delete the attachment.
	if err := store.Delete(context.Background(), "session-b", victim.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-session Delete error = %v, want ErrNotFound", err)
	}
	if _, err := os.Lstat(filepath.Join(store.Root(), victim.Path)); err != nil {
		t.Fatalf("rejected Delete removed the attachment: %v", err)
	}
	// An empty session key is refused everywhere.
	calls := []func() error{
		func() error { return store.Delete(context.Background(), " ", victim.ID) },
		func() error { _, err := store.List(context.Background(), " "); return err },
		func() error { _, _, err := store.Get(context.Background(), " ", victim.ID); return err },
		func() error {
			_, err := store.Put(context.Background(), " ", "x.txt", strings.NewReader("y"))
			return err
		},
		func() error { _, err := store.Manifest(context.Background(), " "); return err },
	}
	before := dirNames(t, store.Root())
	for i, call := range calls {
		if err := call(); !errors.Is(err, ErrEmptySessionKey) {
			t.Fatalf("call %d empty session key error = %v, want ErrEmptySessionKey", i, err)
		}
	}
	// An empty session key is refused before any directory is created, so the
	// store keeps only the session that was actually written.
	if got := dirNames(t, store.Root()); !equalNames(before, got) {
		t.Fatalf("empty session key wrote to the store root: before=%v after=%v", before, got)
	}
}

func TestPutOverwritesNothingOnManifestFailure(t *testing.T) {
	store := newStore(t, Limits{MaxFileBytes: 1 << 20, MaxSessionBytes: 4 << 20, TTL: time.Hour})
	ref := mustPut(t, store, testSession, "one.txt", []byte("first"))
	// Corrupt the manifest so the next Put cannot record itself.
	manifestPath := filepath.Join(store.Root(), encodeSessionKey(testSession), "manifest.json")
	if err := os.WriteFile(manifestPath, []byte("{oops"), 0o600); err != nil {
		t.Fatalf("corrupt manifest: %v", err)
	}
	if _, err := store.Put(context.Background(), testSession, "two.txt", strings.NewReader("second")); err == nil {
		t.Fatal("Put succeeded over a corrupt manifest")
	}
	// The already stored attachment is untouched; only the rejected temp file
	// is gone.
	entries, err := os.ReadDir(filepath.Join(store.Root(), encodeSessionKey(testSession), "files"))
	if err != nil {
		t.Fatalf("read files dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != ref.ID {
		t.Fatalf("files after failed Put = %v", entries)
	}
}

func encodeFileIDForTest(t *testing.T) string {
	t.Helper()
	id, err := newFileID()
	if err != nil {
		t.Fatalf("newFileID: %v", err)
	}
	return id
}

func backdateManifest(path, id string, when time.Time) error {
	manifest, err := readManifest(path)
	if err != nil {
		return err
	}
	found := false
	for i := range manifest.Files {
		if manifest.Files[i].ID == id {
			manifest.Files[i].CreatedAt = when
			found = true
		}
	}
	if !found {
		return errors.New("manifest entry not found")
	}
	return writeManifest(path, manifest)
}
