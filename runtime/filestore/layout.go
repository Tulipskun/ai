package filestore

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const (
	// DefaultRoot is the attachment store location relative to the runtime
	// state root, required by CON-011.
	DefaultRoot = "data/attachments"

	filesDirName    = "files"
	manifestName    = "manifest.json"
	tmpPrefix       = ".tmp-"
	manifestVersion = 1

	// sniffBytes is how much of an attachment is buffered to detect its
	// content type. It is also the largest empty-file read allowance.
	sniffBytes = 512

	// Fallback bounds used when a configuration leaves a value out.
	defaultMaxFileBytes    = int64(25) << 20
	defaultMaxSessionBytes = int64(100) << 20
	defaultTTL             = 7 * 24 * time.Hour

	maxEncodedSessionKey = 256
	maxNameLen           = 160
	fallbackName         = "attachment"
)

// Manifest is the per-session metadata index. It lives next to the session's
// files inside the attachment store, never in the session database (CON-002,
// CON-003).
type Manifest struct {
	Version int    `json:"version"`
	Files   []Meta `json:"files"`
}

func newManifest() Manifest { return Manifest{Version: manifestVersion} }

// usedBytes is the attachment payload accounted against MaxSessionBytes. The
// manifest itself is excluded so accounting matches the stored files.
func (m Manifest) usedBytes() int64 {
	var total int64
	for _, item := range m.Files {
		if item.Size > 0 {
			total += item.Size
		}
	}
	return total
}

func (m Manifest) lookup(id string) (Meta, bool) {
	for _, item := range m.Files {
		if item.ID == id {
			return item, true
		}
	}
	return Meta{}, false
}

func readManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return newManifest(), nil
		}
		return Manifest{}, fmt.Errorf("filestore: read manifest %q: %w", path, err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("filestore: decode manifest %q: %w", path, err)
	}
	if manifest.Version == 0 {
		manifest.Version = manifestVersion
	}
	return manifest, nil
}

func writeManifest(path string, manifest Manifest) error {
	if manifest.Version == 0 {
		manifest.Version = manifestVersion
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("filestore: encode manifest: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("filestore: create manifest directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), tmpPrefix+"manifest-*.json")
	if err != nil {
		return fmt.Errorf("filestore: create temp manifest: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("filestore: write temp manifest: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("filestore: sync temp manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("filestore: close temp manifest: %w", err)
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return fmt.Errorf("filestore: chmod manifest: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("filestore: replace manifest: %w", err)
	}
	return nil
}

// encodeSessionKey maps a raw session identity to a path-safe directory name.
// It mirrors the base64url encoding used by sdk.SessionDBPath so a raw Discord
// or CLI session ID is never concatenated into a path.
func encodeSessionKey(sessionKey string) string {
	trimmed := strings.TrimSpace(sessionKey)
	if trimmed == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(trimmed))
}

// validSessionKeyDir reports whether a directory name under the store root can
// have been produced by encodeSessionKey. Anything else is ignored by cleanup.
func validSessionKeyDir(name string) bool {
	if name == "" || len(name) > maxEncodedSessionKey {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(name)
	if err != nil {
		return false
	}
	return base64.RawURLEncoding.EncodeToString(raw) == name
}

// newFileID returns an unguessable, path-safe identifier for one attachment.
func newFileID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("filestore: generate attachment id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// validFileID reports whether id is a bare attachment filename: base64url of
// exactly 16 random bytes, with no separators or traversal characters.
func validFileID(id string) bool {
	if len(id) != 22 {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil || len(raw) != 16 {
		return false
	}
	return base64.RawURLEncoding.EncodeToString(raw) == id
}

// sanitizeName turns a caller-supplied filename (for example a Discord
// attachment name) into display metadata only. It never takes part in path
// construction.
func sanitizeName(name string) string {
	trimmed := strings.TrimSpace(name)
	// Take the last segment of either separator style: the name is display
	// metadata only and must never contribute path structure.
	if index := strings.LastIndexAny(trimmed, `/\`); index >= 0 {
		trimmed = trimmed[index+1:]
	}
	base := filepath.Base(trimmed)
	var builder strings.Builder
	for _, r := range base {
		if r == '/' || r == '\\' || r == ':' || unicode.IsControl(r) {
			builder.WriteRune('_')
			continue
		}
		builder.WriteRune(r)
	}
	cleaned := strings.TrimSpace(builder.String())
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return fallbackName
	}
	if len(cleaned) > maxNameLen {
		return cleaned[len(cleaned)-maxNameLen:]
	}
	return cleaned
}

// detectContentType classifies an attachment from its first bytes only.
func detectContentType(snippet []byte) string {
	if len(snippet) == 0 {
		return "application/octet-stream"
	}
	if ct := http.DetectContentType(snippet); ct != "application/octet-stream" {
		return ct
	}
	return "application/octet-stream"
}
