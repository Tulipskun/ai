package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Tulipskun/ai/runtime/filestore"
)

// AttachmentConfig is the config/attachment.json view of the attachment file
// store. It mirrors the browser config precedent in this package: the root is
// relative to the runtime state root and every limit lives in config, never in
// an environment variable (CON-001, REQ-011, CON-011).
//
// The storage fields decide what the file store accepts; the transfer fields
// decide how much time and how many bytes the transport may spend on one file in
// each direction. They are kept in one file because they describe one subsystem,
// and because the timeout that bounds an upload must be resolvable without
// touching the harness display timeout (see the note on UploadTimeout).
type AttachmentConfig struct {
	Enabled         bool          `json:"enabled"`
	Root            string        `json:"root"`
	MaxFileBytes    int64         `json:"max_file_bytes"`
	MaxSessionBytes int64         `json:"max_session_bytes"`
	TTL             time.Duration `json:"-"`

	// DownloadTimeout bounds one inbound attachment fetch and UploadTimeout
	// bounds one outbound multipart send. Both are deliberately separate from
	// the harness display timeout: a 10 s text-reply budget must not kill the
	// upload of a design file, and raising the upload budget must not raise the
	// text-reply budget for the whole system. The transport applies them on its
	// own derived context, so a zero value here simply keeps the transport
	// default rather than meaning "no limit".
	DownloadTimeout time.Duration `json:"-"`
	UploadTimeout   time.Duration `json:"-"`
	// MaxSendFileBytes and MaxSendFileCount bound one outbound message. They are
	// their own fields because Discord's per-upload ceiling is not the same
	// number as the size the store is willing to accept on inbound.
	MaxSendFileBytes int64 `json:"max_send_file_bytes"`
	MaxSendFileCount int   `json:"max_send_file_count"`
}

// Transport-side fallbacks for the file-transfer budgets this configuration
// owns. They mirror the transport defaults, so a caller that passes only what it
// configured still gets a bounded pipeline, and a config that omits these keys
// behaves exactly as it did before the keys existed.
const (
	DefaultAttachmentDownloadTimeout = 30 * time.Second
	DefaultAttachmentUploadTimeout   = 2 * time.Minute
	// DefaultAttachmentMaxSendFileBytes is Discord's ordinary per-upload ceiling
	// for a normal account, so a file the transport sends is refused locally
	// with a readable note instead of failing as an opaque REST error.
	DefaultAttachmentMaxSendFileBytes = int64(8) << 20
	DefaultAttachmentMaxSendFiles     = 10
)

const DefaultAttachmentConfigPath = "config/attachment.json"

// errAttachmentRootRequired is the sentinel for a config that sets "root" to a
// blank value. Load and Validate return it so callers can distinguish a
// misconfiguration from other failures.
var errAttachmentRootRequired = errors.New("attachment: root is required")

// defaultAttachmentConfig returns the CON-011 fallback layout: the attachment
// store under data/attachments of the state root, with bounded sizes and a
// one-week TTL. The transfer budgets stay zero, which delegates them to the
// transport defaults above; writing them into the store config would make two
// packages disagree about one number.
func defaultAttachmentConfig() AttachmentConfig {
	limits := filestore.DefaultLimits()
	return AttachmentConfig{
		Enabled:         true,
		Root:            filestore.DefaultRoot,
		MaxFileBytes:    limits.MaxFileBytes,
		MaxSessionBytes: limits.MaxSessionBytes,
		TTL:             limits.TTL,
	}
}

// LoadAttachmentConfig reads attachment settings, falling back to safe
// defaults when the file is absent.
func LoadAttachmentConfig(path string) (AttachmentConfig, error) {
	if path == "" {
		path = DefaultAttachmentConfigPath
	}
	cfg := defaultAttachmentConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return AttachmentConfig{}, fmt.Errorf("attachment: read config %q: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return AttachmentConfig{}, fmt.Errorf("attachment: decode config %q: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return AttachmentConfig{}, err
	}
	return cfg, nil
}

// SaveAttachmentConfig writes attachment settings, replacing zero values with
// defaults so a later load stays valid.
func SaveAttachmentConfig(path string, cfg AttachmentConfig) error {
	if path == "" {
		path = DefaultAttachmentConfigPath
	}
	defaults := defaultAttachmentConfig()
	// A blank root on save means "write the default", mirroring LoadAttachmentConfig.
	// A config file that explicitly sets "root": "" is still rejected by Validate.
	root := strings.TrimSpace(cfg.Root)
	if root == "" {
		root = defaults.Root
	}
	maxFile, maxSession, ttl := cfg.MaxFileBytes, cfg.MaxSessionBytes, cfg.TTL
	if maxFile <= 0 {
		maxFile = defaults.MaxFileBytes
	}
	if maxSession <= 0 {
		maxSession = defaults.MaxSessionBytes
	}
	if ttl <= 0 {
		ttl = defaults.TTL
	}
	if maxSession < maxFile {
		return fmt.Errorf("attachment: max_session_bytes %d is smaller than max_file_bytes %d", maxSession, maxFile)
	}
	// Only the transfer budgets are re-checked here. The storage fields above are
	// already normalised onto defaults, and a zero-valued config is legal to save
	// (it writes the defaults with "enabled": false), so requiring a root would
	// break that path.
	transfer := AttachmentConfig{
		Root:             root,
		MaxFileBytes:     maxFile,
		MaxSessionBytes:  maxSession,
		TTL:              ttl,
		DownloadTimeout:  cfg.DownloadTimeout,
		UploadTimeout:    cfg.UploadTimeout,
		MaxSendFileBytes: cfg.MaxSendFileBytes,
		MaxSendFileCount: cfg.MaxSendFileCount,
	}
	if err := transfer.Validate(); err != nil {
		return err
	}
	out := map[string]any{
		"enabled":           cfg.Enabled,
		"root":              root,
		"max_file_bytes":    maxFile,
		"max_session_bytes": maxSession,
		"ttl":               ttl.String(),
	}
	// The transfer budgets are written only when the caller set them, so a saved
	// config keeps the transport fallbacks instead of freezing today's defaults
	// into the user's file.
	if cfg.DownloadTimeout > 0 {
		out["download_timeout"] = cfg.DownloadTimeout.String()
	}
	if cfg.UploadTimeout > 0 {
		out["upload_timeout"] = cfg.UploadTimeout.String()
	}
	if cfg.MaxSendFileBytes > 0 {
		out["max_send_file_bytes"] = cfg.MaxSendFileBytes
	}
	if cfg.MaxSendFileCount > 0 {
		out["max_send_file_count"] = cfg.MaxSendFileCount
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("attachment: encode config: %w", err)
	}
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		return fmt.Errorf("attachment: create config directory: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("attachment: write config %q: %w", path, err)
	}
	return nil
}

// Validate rejects nonsensical settings before they reach the store or the
// transport.
func (c AttachmentConfig) Validate() error {
	if strings.TrimSpace(c.Root) == "" {
		return errAttachmentRootRequired
	}
	if c.MaxFileBytes < 0 {
		return fmt.Errorf("attachment: max_file_bytes must not be negative, got %d", c.MaxFileBytes)
	}
	if c.MaxSessionBytes < 0 {
		return fmt.Errorf("attachment: max_session_bytes must not be negative, got %d", c.MaxSessionBytes)
	}
	if c.TTL < 0 {
		return fmt.Errorf("attachment: ttl must not be negative, got %s", c.TTL)
	}
	if c.MaxFileBytes > 0 && c.MaxSessionBytes > 0 && c.MaxSessionBytes < c.MaxFileBytes {
		return fmt.Errorf("attachment: max_session_bytes %d is smaller than max_file_bytes %d", c.MaxSessionBytes, c.MaxFileBytes)
	}
	if c.DownloadTimeout < 0 {
		return fmt.Errorf("attachment: download_timeout must not be negative, got %s", c.DownloadTimeout)
	}
	if c.UploadTimeout < 0 {
		return fmt.Errorf("attachment: upload_timeout must not be negative, got %s", c.UploadTimeout)
	}
	if c.MaxSendFileBytes < 0 {
		return fmt.Errorf("attachment: max_send_file_bytes must not be negative, got %d", c.MaxSendFileBytes)
	}
	if c.MaxSendFileCount < 0 {
		return fmt.Errorf("attachment: max_send_file_count must not be negative, got %d", c.MaxSendFileCount)
	}
	return nil
}

// Limits converts the configuration into store bounds, replacing zero fields
// with defaults so a partial config stays usable.
func (c AttachmentConfig) Limits() filestore.Limits {
	defaults := filestore.DefaultLimits()
	limits := filestore.Limits{MaxFileBytes: c.MaxFileBytes, MaxSessionBytes: c.MaxSessionBytes, TTL: c.TTL}
	if limits.MaxFileBytes == 0 {
		limits.MaxFileBytes = defaults.MaxFileBytes
	}
	if limits.MaxSessionBytes == 0 {
		limits.MaxSessionBytes = defaults.MaxSessionBytes
	}
	if limits.TTL == 0 {
		limits.TTL = defaults.TTL
	}
	return limits
}

// Open prepares the attachment store described by this configuration under the
// runtime state root. A disabled configuration yields no store and no error,
// which lets callers skip attachment handling instead of failing.
func (c AttachmentConfig) Open(stateRoot string) (*filestore.Store, error) {
	if !c.Enabled {
		return nil, nil
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return filestore.OpenUnder(stateRoot, c.Root, c.Limits())
}

// UnmarshalJSON fills omitted fields from defaults and parses the duration
// strings, matching how browser timeouts are configured.
func (c *AttachmentConfig) UnmarshalJSON(data []byte) error {
	defaults := defaultAttachmentConfig()
	type raw struct {
		Enabled          *bool   `json:"enabled"`
		Root             *string `json:"root"`
		MaxFileBytes     int64   `json:"max_file_bytes"`
		MaxSessionBytes  int64   `json:"max_session_bytes"`
		TTL              string  `json:"ttl"`
		DownloadTimeout  string  `json:"download_timeout"`
		UploadTimeout    string  `json:"upload_timeout"`
		MaxSendFileBytes int64   `json:"max_send_file_bytes"`
		MaxSendFileCount int     `json:"max_send_file_count"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	*c = defaults
	if r.Enabled != nil {
		c.Enabled = *r.Enabled
	}
	// An omitted "root" keeps the CON-011 default. A root that is present but
	// blank is a misconfiguration, so it is left empty and Validate rejects it
	// instead of silently relocating the store.
	if r.Root != nil {
		c.Root = strings.TrimSpace(*r.Root)
	}
	if r.MaxFileBytes != 0 {
		c.MaxFileBytes = r.MaxFileBytes
	}
	if r.MaxSessionBytes != 0 {
		c.MaxSessionBytes = r.MaxSessionBytes
	}
	// A blank ttl keeps the default, while a ttl that is present but not
	// positive is a misconfiguration: it is refused instead of silently
	// reverting to a week.
	ttl, err := parseAttachmentDuration("ttl", r.TTL)
	if err != nil {
		return err
	}
	if ttl > 0 {
		c.TTL = ttl
	}
	// The transfer budgets follow the same rule: unset keeps the transport
	// default (zero), a value that is present must be positive.
	download, err := parseAttachmentDuration("download_timeout", r.DownloadTimeout)
	if err != nil {
		return err
	}
	c.DownloadTimeout = download
	upload, err := parseAttachmentDuration("upload_timeout", r.UploadTimeout)
	if err != nil {
		return err
	}
	c.UploadTimeout = upload
	c.MaxSendFileBytes = r.MaxSendFileBytes
	c.MaxSendFileCount = r.MaxSendFileCount
	return nil
}

// parseAttachmentDuration reads one duration setting. A blank value means "not
// set" and yields zero, which lets a caller fall back to its own default; a
// value that is present must be positive, because a zero or negative budget
// would silently disable a transfer rather than configure it.
func parseAttachmentDuration(field, value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("attachment: %s: %w", field, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("attachment: %s must be positive, got %s", field, duration)
	}
	return duration, nil
}
