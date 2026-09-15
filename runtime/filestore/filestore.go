// Package filestore stores attachment files under the runtime state root.
//
// It owns the on-disk layout required by CON-011: one directory per session
// under the attachment root, a JSON manifest per session, per-file and
// per-session size limits, and TTL cleanup. It returns opaque references
// (name, content type, size, relative path) only; file content never leaves
// this package through a reference, which keeps REQ-016 and REQ-019 satisfied.
//
// The package depends on neither a transport nor a provider, and it never
// resolves paths against the workspace or the session database directory.
package filestore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Limits carries the size and lifetime bounds for one attachment store.
type Limits struct {
	MaxFileBytes    int64
	MaxSessionBytes int64
	TTL             time.Duration
}

// DefaultLimits returns the fallback bounds used when a configuration omits a
// value. Zero fields in Limits are replaced by these defaults.
func DefaultLimits() Limits {
	return Limits{
		MaxFileBytes:    defaultMaxFileBytes,
		MaxSessionBytes: defaultMaxSessionBytes,
		TTL:             defaultTTL,
	}
}

func (l Limits) normalize() (Limits, error) {
	defaults := DefaultLimits()
	if l.MaxFileBytes == 0 {
		l.MaxFileBytes = defaults.MaxFileBytes
	}
	if l.MaxSessionBytes == 0 {
		l.MaxSessionBytes = defaults.MaxSessionBytes
	}
	if l.TTL == 0 {
		l.TTL = defaults.TTL
	}
	if l.MaxFileBytes < 0 {
		return Limits{}, fmt.Errorf("filestore: max file size must not be negative, got %d", l.MaxFileBytes)
	}
	if l.MaxSessionBytes < 0 {
		return Limits{}, fmt.Errorf("filestore: max session size must not be negative, got %d", l.MaxSessionBytes)
	}
	if l.TTL < 0 {
		return Limits{}, fmt.Errorf("filestore: ttl must not be negative, got %s", l.TTL)
	}
	if l.MaxSessionBytes < l.MaxFileBytes {
		return Limits{}, fmt.Errorf("filestore: max session size %d is smaller than max file size %d", l.MaxSessionBytes, l.MaxFileBytes)
	}
	return l, nil
}

// Meta is the stored record for one attachment. It carries metadata only; it
// never contains file content.
type Meta struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	Path        string    `json:"path"`
	CreatedAt   time.Time `json:"created_at"`
}

// Ref is the opaque handle handed to transports and agents (REQ-016).
type Ref struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Path        string `json:"path"`
}

// Ref returns the opaque view of a stored attachment.
func (m Meta) Ref() Ref {
	return Ref{ID: m.ID, Name: m.Name, ContentType: m.ContentType, Size: m.Size, Path: m.Path}
}

var (
	// ErrFileTooLarge reports an attachment above Limits.MaxFileBytes.
	ErrFileTooLarge = errors.New("filestore: attachment exceeds the per-file size limit")
	// ErrSessionTooLarge reports an attachment that would push a session above Limits.MaxSessionBytes.
	ErrSessionTooLarge = errors.New("filestore: attachment exceeds the session size limit")
	// ErrNotFound reports an unknown attachment reference for a session.
	ErrNotFound = errors.New("filestore: attachment not found")
	// ErrInvalidRef reports a malformed or escaping attachment reference.
	ErrInvalidRef = errors.New("filestore: invalid attachment reference")
	// ErrEmptySessionKey reports a missing session identity.
	ErrEmptySessionKey = errors.New("filestore: session key is required")
)

// Store is one attachment store rooted at a single directory.
type Store struct {
	root   string
	limits Limits

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// Open prepares an attachment store rooted at root. The root is made absolute
// and cleaned, checked against symlinks, then created with 0700 and
// canonicalized. Zero-valued limits fall back to DefaultLimits. Callers that
// must pin the store inside the state root should use OpenUnder.
//
// Every check runs before the first filesystem write: a root that is rejected
// never leaves a directory behind.
func Open(root string, limits Limits) (*Store, error) {
	normalized, err := limits.normalize()
	if err != nil {
		return nil, err
	}
	abs, err := absClean(root)
	if err != nil {
		return nil, err
	}
	// Resolve the candidate read-only, before MkdirAll: a store root that is
	// itself a symlink pointing elsewhere is rejected instead of being created
	// and written through.
	target, err := resolveCandidate("", abs)
	if err != nil {
		return nil, fmt.Errorf("filestore: attachment store %q: %w", root, err)
	}
	return openValidated("", target, normalized)
}

// OpenUnder prepares an attachment store for dir relative to the runtime state
// root, refusing roots that escape the state root or that land on the shared
// data directory or the session databases (CON-011, CON-002, CON-003). An
// empty dir is the intentional default and resolves to DefaultRoot.
//
// Placement, traversal, and symlink checks all run before any directory is
// created, so a rejected root cannot write outside the state root.
func OpenUnder(stateRoot, dir string, limits Limits) (*Store, error) {
	normalized, err := limits.normalize()
	if err != nil {
		return nil, err
	}
	base, err := absClean(stateRoot)
	if err != nil {
		return nil, err
	}
	// Resolve the state root read-only, including a not-yet-created root whose
	// ancestors are symlinks, so placement checks compare canonical paths.
	base, err = resolveExistingPath(base)
	if err != nil {
		return nil, fmt.Errorf("filestore: resolve state root %q: %w", stateRoot, err)
	}
	requested := strings.TrimSpace(dir)
	if requested == "" {
		requested = DefaultRoot
	}
	joined := requested
	if !filepath.IsAbs(joined) {
		joined = filepath.Join(base, joined)
	}
	abs, err := absClean(joined)
	if err != nil {
		return nil, err
	}
	// Placement and symlink checks are pure reads and both run before the
	// directory is created, so a rejected root cannot write outside the state
	// root.
	target, err := resolveCandidate(base, abs)
	if err != nil {
		return nil, fmt.Errorf("filestore: attachment store %q: %w", dir, err)
	}
	return openValidated(base, target, normalized)
}

// resolveCandidate applies the placement rules and symlink resolution to a
// store root without touching the filesystem. boundary may be empty when the
// caller has no enclosing state root to enforce.
func resolveCandidate(boundary, abs string) (string, error) {
	if err := verifyPlacement(boundary, abs); err != nil {
		return "", err
	}
	target, err := resolveExistingPath(abs)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	if !withinRoot(abs, target) {
		return "", fmt.Errorf("%q resolves outside itself", abs)
	}
	if boundary != "" && !withinRoot(boundary, target) {
		return "", fmt.Errorf("%q resolves outside %q through a symlink", abs, boundary)
	}
	return target, nil
}

// openValidated creates the store directory for an already validated
// candidate. stateRoot may be empty when the caller has no enclosing root to
// enforce; the checks are repeated after creation so a concurrent symlink swap
// cannot place the store outside the state root.
func openValidated(stateRoot, abs string, limits Limits) (*Store, error) {
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("filestore: create attachment store %q: %w", abs, err)
	}
	resolved, err := canonical(abs)
	if err != nil {
		return nil, fmt.Errorf("filestore: resolve attachment store %q: %w", abs, err)
	}
	if !withinRoot(abs, resolved) {
		return nil, fmt.Errorf("filestore: attachment store %q resolves outside itself", abs)
	}
	if stateRoot != "" {
		if err := verifyPlacement(stateRoot, resolved); err != nil {
			return nil, err
		}
	}
	return &Store{root: resolved, limits: limits, locks: make(map[string]*sync.Mutex)}, nil
}

// verifyPlacement applies the CON-011 placement rules with pure path arithmetic
// so it can reject a root before anything is written. An empty stateRoot
// disables the check.
func verifyPlacement(stateRoot, root string) error {
	if stateRoot == "" {
		return nil
	}
	if root == stateRoot {
		return fmt.Errorf("filestore: attachment store must be a subdirectory of the state root %q, not the state root itself", stateRoot)
	}
	if !withinRoot(stateRoot, root) {
		return fmt.Errorf("filestore: attachment store %q must be under state root %q", root, stateRoot)
	}
	dataDir := filepath.Join(stateRoot, "data")
	if root == dataDir {
		return fmt.Errorf("filestore: attachment store %q must not be the shared data directory", root)
	}
	sessionDir := sessionDataDir(stateRoot)
	if withinRoot(sessionDir, root) {
		return fmt.Errorf("filestore: attachment store %q must not be inside the session data directory %q", root, sessionDir)
	}
	return nil
}

// canonical resolves symlinks for an existing path and returns an absolute,
// cleaned result. It never creates anything.
func canonical(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Clean(resolved))
}

func sessionDataDir(stateRoot string) string {
	return filepath.Join(stateRoot, "data", "sessions")
}

// Root returns the canonical attachment store root directory.
func (s *Store) Root() string { return s.root }

// Limits returns the effective bounds of the store.
func (s *Store) Limits() Limits { return s.limits }

// Put stores one attachment for a session and returns its opaque reference.
// The reader is streamed, never buffered in memory beyond one read chunk, and
// is rejected when it exceeds the per-file or per-session limit.
func (s *Store) Put(ctx context.Context, sessionKey, name string, r io.Reader) (Ref, error) {
	return s.PutWithContentType(ctx, sessionKey, name, "", r)
}

// PutWithContentType stores one attachment and records contentType instead of
// detecting it from the first bytes. An empty contentType enables detection.
func (s *Store) PutWithContentType(ctx context.Context, sessionKey, name, contentType string, r io.Reader) (Ref, error) {
	if r == nil {
		return Ref{}, errors.New("filestore: attachment reader is required")
	}
	if err := ctx.Err(); err != nil {
		return Ref{}, err
	}
	key, sessionDir, err := s.sessionDir(sessionKey)
	if err != nil {
		return Ref{}, err
	}
	// The session lock is an in-memory guard, so taking it before the first
	// write keeps the accounting below consistent without touching the disk.
	unlock := s.lockSession(key)
	defer unlock()
	// Everything up to the first filesystem write is read-only: the manifest,
	// the budget, and the sniffed head. An upload that is refused here leaves
	// no session directory, no temp file, and no manifest entry behind.
	manifest, err := readManifest(s.manifestPath(sessionDir))
	if err != nil {
		return Ref{}, err
	}
	budget := s.limits.MaxSessionBytes - manifest.usedBytes()
	if budget <= 0 {
		return Ref{}, fmt.Errorf("%w: session %q already holds %d bytes", ErrSessionTooLarge, sessionKey, manifest.usedBytes())
	}
	// A single attachment may not exceed the per-file limit, nor whatever the
	// session budget still allows. One extra byte is read so an oversized
	// stream is detected instead of silently truncated.
	limit := s.limits.MaxFileBytes
	if budget < limit {
		limit = budget
	}
	head := make([]byte, sniffBytes)
	n, readErr := io.ReadFull(r, head)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return Ref{}, fmt.Errorf("filestore: read attachment %q: %w", name, readErr)
	}
	head = head[:n]
	if int64(len(head)) > s.limits.MaxFileBytes {
		return Ref{}, fmt.Errorf("%w: %q is larger than %d bytes", ErrFileTooLarge, sanitizeName(name), s.limits.MaxFileBytes)
	}
	if int64(len(head)) > budget {
		return Ref{}, fmt.Errorf("%w: session %q allows %d more bytes, attachment is at least %d bytes", ErrSessionTooLarge, sessionKey, budget, len(head))
	}
	remaining := limit - int64(len(head)) + 1
	if remaining < 0 {
		remaining = 0
	}
	filesDir := filepath.Join(sessionDir, filesDirName)
	if err := os.MkdirAll(filesDir, 0o700); err != nil {
		return Ref{}, fmt.Errorf("filestore: create attachment directory: %w", err)
	}
	id, err := newFileID()
	if err != nil {
		return Ref{}, err
	}
	finalPath := filepath.Join(filesDir, id)
	tmp, err := os.CreateTemp(filesDir, tmpPrefix+"*")
	if err != nil {
		return Ref{}, fmt.Errorf("filestore: create temp attachment: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	size, copyErr := io.Copy(tmp, io.MultiReader(bytes.NewReader(head), io.LimitReader(r, remaining)))
	if copyErr != nil {
		_ = tmp.Close()
		return Ref{}, fmt.Errorf("filestore: write attachment %q: %w", name, copyErr)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return Ref{}, fmt.Errorf("filestore: sync attachment %q: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return Ref{}, fmt.Errorf("filestore: close attachment %q: %w", name, err)
	}
	if size > s.limits.MaxFileBytes {
		return Ref{}, fmt.Errorf("%w: %q is larger than %d bytes", ErrFileTooLarge, sanitizeName(name), s.limits.MaxFileBytes)
	}
	if size > budget {
		return Ref{}, fmt.Errorf("%w: session %q allows %d more bytes, attachment is %d bytes", ErrSessionTooLarge, sessionKey, budget, size)
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = detectContentType(head)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return Ref{}, fmt.Errorf("filestore: store attachment %q: %w", name, err)
	}
	rel, err := filepath.Rel(s.root, finalPath)
	if err != nil {
		_ = os.Remove(finalPath)
		return Ref{}, fmt.Errorf("filestore: relate attachment path: %w", err)
	}
	meta := Meta{
		ID:          id,
		Name:        sanitizeName(name),
		ContentType: strings.TrimSpace(contentType),
		Size:        size,
		Path:        filepath.ToSlash(rel),
		CreatedAt:   time.Now().UTC(),
	}
	manifest.Files = append(manifest.Files, meta)
	if err := writeManifest(s.manifestPath(sessionDir), manifest); err != nil {
		_ = os.Remove(finalPath)
		return Ref{}, err
	}
	return meta.Ref(), nil
}

// Get opens a stored attachment for reading. The reference must exist in the
// session manifest and must resolve to a regular file inside the store root;
// traversal and symlink escapes are rejected.
func (s *Store) Get(ctx context.Context, sessionKey, refID string) (io.ReadCloser, Meta, error) {
	if err := ctx.Err(); err != nil {
		return nil, Meta{}, err
	}
	key, sessionDir, err := s.sessionDir(sessionKey)
	if err != nil {
		return nil, Meta{}, err
	}
	if !validFileID(refID) {
		return nil, Meta{}, fmt.Errorf("%w: %q", ErrInvalidRef, refID)
	}
	manifest, err := readManifest(s.manifestPath(sessionDir))
	if err != nil {
		return nil, Meta{}, err
	}
	meta, ok := manifest.lookup(refID)
	if !ok {
		return nil, Meta{}, fmt.Errorf("%w: %q for session %q", ErrNotFound, refID, key)
	}
	path, err := s.safePath(filepath.Join(s.sessionDirFromKey(key), filesDirName, meta.ID))
	if err != nil {
		return nil, Meta{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, Meta{}, fmt.Errorf("%w: %q for session %q", ErrNotFound, refID, key)
		}
		return nil, Meta{}, fmt.Errorf("filestore: open attachment %q: %w", refID, err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, Meta{}, fmt.Errorf("filestore: stat attachment %q: %w", refID, err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, Meta{}, fmt.Errorf("filestore: attachment %q is not a regular file", refID)
	}
	return file, meta, nil
}

// List returns the stored attachments of one session, oldest first.
func (s *Store) List(ctx context.Context, sessionKey string) ([]Meta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manifest, err := s.Manifest(ctx, sessionKey)
	if err != nil {
		return nil, err
	}
	out := append([]Meta(nil), manifest.Files...)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Manifest returns the metadata manifest of one session. Files listed there
// are metadata only; the manifest never carries attachment content.
func (s *Store) Manifest(ctx context.Context, sessionKey string) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	_, sessionDir, err := s.sessionDir(sessionKey)
	if err != nil {
		return Manifest{}, err
	}
	unlock := s.lockSession(sessionDirKey(sessionDir))
	defer unlock()
	return readManifest(s.manifestPath(sessionDir))
}

// Delete removes one attachment and its manifest record. The session directory
// is dropped once it no longer holds attachments.
func (s *Store) Delete(ctx context.Context, sessionKey, refID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key, sessionDir, err := s.sessionDir(sessionKey)
	if err != nil {
		return err
	}
	if !validFileID(refID) {
		return fmt.Errorf("%w: %q", ErrInvalidRef, refID)
	}
	unlock := s.lockSession(key)
	defer unlock()
	manifest, err := readManifest(s.manifestPath(sessionDir))
	if err != nil {
		return err
	}
	meta, ok := manifest.lookup(refID)
	if !ok {
		return fmt.Errorf("%w: %q for session %q", ErrNotFound, refID, key)
	}
	path, err := s.safePath(filepath.Join(sessionDir, filesDirName, meta.ID))
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("filestore: delete attachment %q: %w", refID, err)
	}
	kept := make([]Meta, 0, len(manifest.Files))
	for _, item := range manifest.Files {
		if item.ID == refID {
			continue
		}
		kept = append(kept, item)
	}
	if len(kept) == 0 {
		return s.removeSession(sessionDir)
	}
	manifest.Files = kept
	return writeManifest(s.manifestPath(sessionDir), manifest)
}

// Cleanup removes attachments older than the configured TTL and returns the
// number of deleted files. now is injected so callers stay testable.
func (s *Store) Cleanup(ctx context.Context, now time.Time) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("filestore: list attachment store: %w", err)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	removed := 0
	var firstErr error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if !entry.IsDir() || !validSessionKeyDir(entry.Name()) {
			continue
		}
		count, err := s.cleanupSession(ctx, entry.Name(), now)
		removed += count
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return removed, firstErr
}

func (s *Store) cleanupSession(ctx context.Context, key string, now time.Time) (int, error) {
	sessionDir := s.sessionDirFromKey(key)
	filesDir := filepath.Join(sessionDir, filesDirName)
	unlock := s.lockSession(key)
	defer unlock()
	manifest, err := readManifest(s.manifestPath(sessionDir))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
		manifest = Manifest{Version: manifestVersion}
	}
	expired := make(map[string]bool, len(manifest.Files))
	kept := make([]Meta, 0, len(manifest.Files))
	removed := 0
	for _, meta := range manifest.Files {
		if !meta.CreatedAt.Add(s.limits.TTL).After(now) {
			expired[meta.ID] = true
			path, err := s.safePath(filepath.Join(filesDir, meta.ID))
			if err != nil {
				if firstErrIsPath(err) {
					return removed, err
				}
				continue
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return removed, fmt.Errorf("filestore: delete expired attachment %q: %w", meta.ID, err)
			}
			removed++
			continue
		}
		kept = append(kept, meta)
	}
	if len(kept) == 0 {
		orphanCount, err := s.removeFileDir(filesDir, nil, now)
		if err != nil {
			return removed, err
		}
		removed += orphanCount
		if err := s.removeSession(sessionDir); err != nil {
			return removed, err
		}
		return removed, nil
	}
	orphanCount, err := s.removeFileDir(filesDir, keepSet(kept), now)
	if err != nil {
		return removed, err
	}
	removed += orphanCount
	if len(kept) == len(manifest.Files) && orphanCount == 0 {
		return removed, nil
	}
	manifest.Files = kept
	if err := writeManifest(s.manifestPath(sessionDir), manifest); err != nil {
		return removed, err
	}
	return removed, nil
}

// removeFileDir deletes files under filesDir that are not in keep and are older
// than the TTL. A nil keep set treats every file as unreferenced, which sweeps
// temp files and renamed-but-unrecorded attachments left by a crash.
func (s *Store) removeFileDir(filesDir string, keep map[string]bool, now time.Time) (int, error) {
	entries, err := os.ReadDir(filesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("filestore: list attachment directory: %w", err)
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if keep != nil && keep[name] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return removed, fmt.Errorf("filestore: stat attachment %q: %w", name, err)
		}
		if !info.ModTime().Add(s.limits.TTL).Before(now) {
			continue
		}
		path, err := s.safePath(filepath.Join(filesDir, name))
		if err != nil {
			if firstErrIsPath(err) {
				return removed, err
			}
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, fmt.Errorf("filestore: delete stale attachment %q: %w", name, err)
		}
		removed++
	}
	return removed, nil
}

func keepSet(metas []Meta) map[string]bool {
	keep := make(map[string]bool, len(metas))
	for _, meta := range metas {
		keep[meta.ID] = true
	}
	return keep
}

func firstErrIsPath(err error) bool {
	return errors.Is(err, ErrInvalidRef) || errors.Is(err, ErrEmptySessionKey)
}

func (s *Store) removeSession(sessionDir string) error {
	if err := os.RemoveAll(sessionDir); err != nil {
		return fmt.Errorf("filestore: remove empty session store: %w", err)
	}
	return nil
}

// sessionDir maps a raw session identity onto the sanitized directory that
// holds its attachments. The identity itself is only ever used as an opaque
// value; the directory name is base64url, matching sdk.SessionDBPath.
func (s *Store) sessionDir(sessionKey string) (string, string, error) {
	key := encodeSessionKey(sessionKey)
	if key == "" {
		return "", "", ErrEmptySessionKey
	}
	return key, s.sessionDirFromKey(key), nil
}

func (s *Store) sessionDirFromKey(key string) string { return filepath.Join(s.root, key) }

func (s *Store) manifestPath(sessionDir string) string {
	return filepath.Join(sessionDir, manifestName)
}

func (s *Store) lockSession(key string) func() {
	if key == "" {
		key = sessionDirKey(key)
	}
	s.mu.Lock()
	lock, ok := s.locks[key]
	if !ok {
		lock = &sync.Mutex{}
		s.locks[key] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func sessionDirKey(sessionDir string) string { return filepath.Base(sessionDir) }

// safePath applies the same discipline as tools.safePath: absolute, cleaned,
// inside the store root, and still inside it after symlink resolution.
func (s *Store) safePath(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty path", ErrInvalidRef)
	}
	candidate := name
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(s.root, candidate)
	}
	abs, err := filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return "", fmt.Errorf("filestore: resolve attachment path: %w", err)
	}
	if !withinRoot(s.root, abs) {
		return "", fmt.Errorf("%w: %q escapes the attachment store", ErrInvalidRef, name)
	}
	resolved, err := resolveExistingPath(abs)
	if err != nil {
		return "", fmt.Errorf("filestore: resolve attachment path %q: %w", name, err)
	}
	if !withinRoot(s.root, resolved) {
		return "", fmt.Errorf("%w: %q escapes the attachment store through a symlink", ErrInvalidRef, name)
	}
	return abs, nil
}

func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func resolveExistingPath(path string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Abs(resolved)
	}
	current := path
	var missing []string
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			resolved, err = filepath.Abs(resolved)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("cannot resolve path: %q", path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func absClean(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("filestore: attachment store root is required")
	}
	abs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("filestore: resolve attachment store root: %w", err)
	}
	if abs == string(filepath.Separator) {
		return "", errors.New("filestore: refusing to use the filesystem root as the attachment store")
	}
	return abs, nil
}
