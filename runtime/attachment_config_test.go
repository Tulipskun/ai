package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tulipskun/ai/runtime/filestore"
)

func TestLoadAttachmentConfigMissingUsesDefaults(t *testing.T) {
	cfg, err := LoadAttachmentConfig(filepath.Join(t.TempDir(), "attachment.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Fatal("attachments should be enabled by default")
	}
	if cfg.Root != filestore.DefaultRoot {
		t.Fatalf("root = %q, want %q", cfg.Root, filestore.DefaultRoot)
	}
	defaults := filestore.DefaultLimits()
	if cfg.MaxFileBytes != defaults.MaxFileBytes || cfg.MaxSessionBytes != defaults.MaxSessionBytes || cfg.TTL != defaults.TTL {
		t.Fatalf("defaults = %+v", cfg)
	}
	if _, err := os.Stat(filepath.Join(t.TempDir(), "attachment.json")); !os.IsNotExist(err) {
		t.Fatal("loading a missing config must not create anything")
	}
}

func TestLoadAttachmentConfigOmittedFieldsFallBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attachment.json")
	if err := os.WriteFile(path, []byte(`{"enabled":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadAttachmentConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	defaults := filestore.DefaultLimits()
	if cfg.Root != filestore.DefaultRoot || cfg.MaxFileBytes != defaults.MaxFileBytes || cfg.TTL != defaults.TTL {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestAttachmentConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "attachment.json")
	want := AttachmentConfig{Enabled: true, Root: "data/attachments", MaxFileBytes: 8 << 20, MaxSessionBytes: 64 << 20, TTL: 12 * time.Hour}
	if err := SaveAttachmentConfig(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAttachmentConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"ttl": "12h0m0s"`) {
		t.Fatalf("ttl not stored as a duration string: %s", data)
	}
}

func TestSaveAttachmentConfigZeroFieldsWritesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attachment.json")
	if err := SaveAttachmentConfig(path, AttachmentConfig{}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAttachmentConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	defaults := filestore.DefaultLimits()
	if got.Enabled {
		t.Fatal("a zero config must not enable the store")
	}
	if got.Root != filestore.DefaultRoot || got.MaxFileBytes != defaults.MaxFileBytes || got.MaxSessionBytes != defaults.MaxSessionBytes || got.TTL != defaults.TTL {
		t.Fatalf("got = %+v", got)
	}
}

func TestSaveAttachmentConfigRejectsInvertedBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attachment.json")
	err := SaveAttachmentConfig(path, AttachmentConfig{Enabled: true, Root: filestore.DefaultRoot, MaxFileBytes: 1 << 30, MaxSessionBytes: 1 << 20, TTL: time.Hour})
	if err == nil {
		t.Fatal("expected inverted bounds error")
	}
}

// TestLoadAttachmentConfigEmptyRootDecisions pins the decided contract: an
// omitted "root" keeps the CON-011 default, while a root that is present but
// blank is a misconfiguration and must be rejected instead of silently
// relocating the store.
func TestLoadAttachmentConfigEmptyRootDecisions(t *testing.T) {
	defaults := filestore.DefaultLimits()
	omitted := filepath.Join(t.TempDir(), "attachment.json")
	if err := os.WriteFile(omitted, []byte(`{"enabled":true,"max_file_bytes":1024}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadAttachmentConfig(omitted)
	if err != nil {
		t.Fatalf("omitted root: %v", err)
	}
	if cfg.Root != filestore.DefaultRoot || cfg.MaxFileBytes != 1024 || cfg.MaxSessionBytes != defaults.MaxSessionBytes || cfg.TTL != defaults.TTL {
		t.Fatalf("omitted root config = %+v", cfg)
	}

	for name, content := range map[string]string{
		"empty string":    `{"enabled":true,"root":""}`,
		"whitespace":      `{"enabled":true,"root":"   "}`,
		"tab and newline": `{"enabled":true,"root":"\t\n"}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "attachment.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadAttachmentConfig(path); !errors.Is(err, errAttachmentRootRequired) {
				t.Fatalf("error = %v, want the root-is-required error", err)
			}
		})
	}

	// A struct built in memory with no root is refused by both Validate and
	// Open, so a caller cannot skip the decision by not loading a file.
	if err := (AttachmentConfig{}).Validate(); err == nil {
		t.Fatal("zero config validated")
	}
	if _, err := (AttachmentConfig{Enabled: true, MaxFileBytes: 1, MaxSessionBytes: 2, TTL: time.Minute}).Open(t.TempDir()); err == nil {
		t.Fatal("Open accepted an enabled config without a root")
	}
	// A disabled config still opens nothing and reports no error.
	if store, err := (AttachmentConfig{MaxFileBytes: 1, MaxSessionBytes: 2, TTL: time.Minute}).Open(t.TempDir()); err != nil || store != nil {
		t.Fatalf("disabled config: store=%v err=%v", store, err)
	}
}

func TestLoadAttachmentConfigRejectsInvalidValues(t *testing.T) {
	for name, content := range map[string]string{
		"negative max_file_bytes":    `{"enabled":true,"max_file_bytes":-1}`,
		"negative max_session_bytes": `{"enabled":true,"max_session_bytes":-1}`,
		"negative ttl":               `{"enabled":true,"ttl":"-1h"}`,
		"zero ttl":                   `{"enabled":true,"ttl":"0s"}`,
		"bad ttl":                    `{"enabled":true,"ttl":"tomorrow"}`,
		"empty root":                 `{"enabled":true,"root":""}`,
		"inverted bounds":            `{"enabled":true,"max_file_bytes":2048,"max_session_bytes":1024}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "attachment.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadAttachmentConfig(path); err == nil {
				t.Fatalf("expected error for %s", content)
			}
		})
	}
}

func TestLoadAttachmentConfigRejectsBadJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attachment.json")
	if err := os.WriteFile(path, []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAttachmentConfig(path); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestAttachmentConfigOpenStoresUnderStateRoot(t *testing.T) {
	state := t.TempDir()
	cfg, err := LoadAttachmentConfig(filepath.Join(state, DefaultAttachmentConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	store, err := cfg.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		t.Fatal("expected a store")
	}
	want := filepath.Join(state, filepath.FromSlash(filestore.DefaultRoot))
	if store.Root() != want {
		t.Fatalf("root = %q, want %q", store.Root(), want)
	}
	info, err := os.Stat(want)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", want)
	}
	if got := store.Limits(); got.TTL != cfg.TTL || got.MaxFileBytes != cfg.MaxFileBytes || got.MaxSessionBytes != cfg.MaxSessionBytes {
		t.Fatalf("limits = %+v, config = %+v", got, cfg)
	}
}

func TestAttachmentConfigOpenDisabled(t *testing.T) {
	store, err := (AttachmentConfig{Root: filestore.DefaultRoot, MaxFileBytes: 1, MaxSessionBytes: 2, TTL: time.Minute}).Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if store != nil {
		t.Fatal("a disabled config must not open a store")
	}
}

func TestAttachmentConfigOpenRejectsEscapingRoot(t *testing.T) {
	cfg, err := LoadAttachmentConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Open(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	cfg.Root = "../outside"
	state := t.TempDir()
	if _, err := cfg.Open(state); err == nil {
		t.Fatal("expected a root escaping the state root to be rejected")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(state), "outside")); !os.IsNotExist(err) {
		t.Fatal("escaping root must not create anything outside the state root")
	}
}

func TestAttachmentConfigLimitsFillsZeroFields(t *testing.T) {
	defaults := filestore.DefaultLimits()
	limits := (AttachmentConfig{}).Limits()
	if limits.MaxFileBytes != defaults.MaxFileBytes || limits.MaxSessionBytes != defaults.MaxSessionBytes || limits.TTL != defaults.TTL {
		t.Fatalf("limits = %+v", limits)
	}
	partial := (AttachmentConfig{MaxFileBytes: 123, TTL: time.Minute}).Limits()
	if partial.MaxFileBytes != 123 || partial.TTL != time.Minute || partial.MaxSessionBytes != defaults.MaxSessionBytes {
		t.Fatalf("partial = %+v", partial)
	}
}

// The transfer budgets are the part of this file the Discord transport reads, so
// their parsing, defaults, and refusal of nonsense are pinned here rather than
// only through the transport.

func TestAttachmentConfigTransferBudgetsFallBackToTransportDefaults(t *testing.T) {
	// Absent keys keep the transport fallbacks by staying zero, which is what
	// lets one default value live in one place.
	path := filepath.Join(t.TempDir(), "attachment.json")
	if err := os.WriteFile(path, []byte(`{"enabled":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadAttachmentConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DownloadTimeout != 0 || cfg.UploadTimeout != 0 || cfg.MaxSendFileBytes != 0 || cfg.MaxSendFileCount != 0 {
		t.Fatalf("unset transfer budgets should stay zero so the transport defaults apply: %+v", cfg)
	}
}

func TestAttachmentConfigTransferBudgetsAreReadAndSaved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attachment.json")
	want := AttachmentConfig{
		Enabled: true, Root: filestore.DefaultRoot,
		MaxFileBytes: 1 << 20, MaxSessionBytes: 2 << 20, TTL: time.Hour,
		DownloadTimeout: 4 * time.Second, UploadTimeout: 7 * time.Minute,
		MaxSendFileBytes: 8 << 20, MaxSendFileCount: 3,
	}
	if err := SaveAttachmentConfig(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAttachmentConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{`"download_timeout": "4s"`, `"upload_timeout": "7m0s"`, `"max_send_file_bytes": 8388608`, `"max_send_file_count": 3`} {
		if !strings.Contains(string(data), needle) {
			t.Fatalf("saved config %s missing %s", data, needle)
		}
	}
}

func TestSaveAttachmentConfigOmitsUnsetTransferBudgets(t *testing.T) {
	// A config that never set them must not freeze today's defaults into the
	// user's file, or the fallback would stop being a fallback.
	path := filepath.Join(t.TempDir(), "attachment.json")
	if err := SaveAttachmentConfig(path, AttachmentConfig{Enabled: true, Root: filestore.DefaultRoot}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"download_timeout", "upload_timeout", "max_send_file_bytes", "max_send_file_count"} {
		if strings.Contains(string(data), needle) {
			t.Fatalf("unset budget %q was written: %s", needle, data)
		}
	}
}

func TestAttachmentConfigRejectsInvalidTransferBudgets(t *testing.T) {
	for name, content := range map[string]string{
		"zero download_timeout": `{"enabled":true,"download_timeout":"0s"}`,
		"negative upload":       `{"enabled":true,"upload_timeout":"-5s"}`,
		"unparsable download":   `{"enabled":true,"download_timeout":"soon"}`,
		"negative send bytes":   `{"enabled":true,"max_send_file_bytes":-1}`,
		"negative send count":   `{"enabled":true,"max_send_file_count":-2}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "attachment.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadAttachmentConfig(path); err == nil {
				t.Fatalf("expected %s to be rejected", content)
			}
		})
	}
	for name, cfg := range map[string]AttachmentConfig{
		"negative download":   {Root: filestore.DefaultRoot, DownloadTimeout: -time.Second},
		"negative upload":     {Root: filestore.DefaultRoot, UploadTimeout: -time.Second},
		"negative send size":  {Root: filestore.DefaultRoot, MaxSendFileBytes: -1},
		"negative send count": {Root: filestore.DefaultRoot, MaxSendFileCount: -1},
	} {
		t.Run("validate "+name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Fatalf("%s passed validation", name)
			}
		})
	}
}
