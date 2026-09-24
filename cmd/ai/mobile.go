package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tulipskun/ai/runtime/d1store"
	"github.com/Tulipskun/ai/sdk"
	mobiletransport "github.com/Tulipskun/ai/transport/mobile"
)

// mobileRuntime owns the stateless-mobile wiring for one daemon process
// (REQ-046). It holds the single credential the system has — the D1 token a
// phone presents — in memory only, hydrates runtime state from D1 once the
// first verified connection arrives, and pushes local state back up (CON-012).
type mobileRuntime struct {
	transport  *mobiletransport.Transport
	client     *d1store.Client
	tokens     *d1store.MemoryToken
	stateRoot  string
	sessionDir string
	cfg        runtimeMobileConfig
	// reloadProviders runs after config is materialized from D1. The runtime
	// loaded providers at boot, before any phone connected, so without this
	// the daemon would know zero providers for its whole lifetime (REQ-046(4)).
	reloadProviders func(context.Context) error
}

type runtimeMobileConfig struct {
	workerBase   string
	listen       string
	publicListen string
	tunnel       bool
	cloudflared  string
	syncConfig   bool
	syncSessions bool
}

func newMobileRuntime(stateRoot, sessionDir string, cfg runtimeMobileConfig, reloadProviders func(context.Context) error) (*mobileRuntime, error) {
	if strings.TrimSpace(cfg.workerBase) == "" {
		return nil, nil
	}
	tokens := d1store.NewMemoryToken()
	client := d1store.NewClient(cfg.workerBase, tokens.Get)
	rt := &mobileRuntime{
		client:          client,
		tokens:          tokens,
		stateRoot:       stateRoot,
		sessionDir:      sessionDir,
		cfg:             cfg,
		reloadProviders: reloadProviders,
	}
	rt.transport = mobiletransport.New(mobiletransport.Config{
		MirrorInput:  rt.mirrorUserTurn,
		Listen:       cfg.listen,
		PublicListen: cfg.publicListen,
		Tunnel:       cfg.tunnel,
		Cloudflared:  cfg.cloudflared,
		Tokens:       tokens,
		WorkerBase:   cfg.workerBase,
		Verifier:     d1storeVerifier{client: client},
		Hydrate:      rt,
		AnnounceURL:  cfg.workerBase,
	})
	return rt, nil
}

// d1storeVerifier adapts the D1 client to the transport's Verifier contract:
// a wrong credential is ErrTokenRejected (counted), anything else is a
// transport problem (503, not counted).
type d1storeVerifier struct{ client *d1store.Client }

func (v d1storeVerifier) VerifyToken(ctx context.Context, token string) error {
	err := v.client.VerifyToken(ctx, token)
	switch {
	case err == nil:
		return nil
	case strings.Contains(err.Error(), d1store.ErrTokenRejected.Error()):
		return mobiletransport.ErrTokenRejected
	default:
		return err
	}
}

// Hydrate implements mobiletransport.Hydrator: it runs once, after the first
// phone hands over a verified token, so a daemon that started with an empty
// state directory still comes up with the operator's config and sessions.
func (m *mobileRuntime) Hydrate(ctx context.Context) error {
	if m == nil || m.client == nil {
		return nil
	}
	if m.cfg.syncConfig {
		report, err := m.client.HydrateConfig(ctx, d1store.DefaultConfigFiles(m.stateRoot))
		if err != nil {
			return err
		}
		if len(report.PulledConfig) > 0 {
			log.Printf("mobile: hydrated config from D1: %s", strings.Join(report.PulledConfig, ", "))
		}
		if m.reloadProviders != nil {
			if err := m.reloadProviders(ctx); err != nil {
				return fmt.Errorf("reload providers after hydrate: %w", err)
			}
		}
	}
	if m.cfg.syncSessions {
		report, err := m.client.HydrateSessions(ctx, m.sessionDir, sessionDBName)
		if err != nil {
			return err
		}
		if len(report.PulledSession) > 0 {
			log.Printf("mobile: hydrated %d session(s) from D1: %s", len(report.PulledSession), strings.Join(report.PulledSession, ", "))
		}
	}
	return nil
}

// PushState uploads the current config and session state. It is best effort:
// a failure here must never interrupt a turn, so the caller only logs it.
func (m *mobileRuntime) PushState(ctx context.Context) {
	if m == nil || m.client == nil {
		return
	}
	if m.cfg.syncConfig {
		report, err := m.client.PushConfig(ctx, d1store.DefaultConfigFiles(m.stateRoot))
		if err != nil {
			log.Printf("mobile: push config to D1: %v", err)
		} else if len(report.PushedConfig) > 0 {
			log.Printf("mobile: pushed config to D1: %s", strings.Join(report.PushedConfig, ", "))
		}
	}
	if m.cfg.syncSessions {
		ids, err := listLocalSessionIDs(m.sessionDir)
		if err != nil {
			return
		}
		report, err := m.client.PushSessions(ctx, m.sessionDir, ids)
		if err != nil {
			log.Printf("mobile: push sessions to D1: %v", err)
		}
		if len(report.SkippedOversize) > 0 {
			log.Printf("mobile: session too large for D1, kept local: %s", strings.Join(report.SkippedOversize, ", "))
		}
	}
}

// PublishOutput sends one finished turn to the phones watching that session and
// mirrors it into D1 in order, so the phone can rebuild the thread after the
// app was closed (REQ-046(5)).
func (m *mobileRuntime) PublishOutput(ctx context.Context, output sdk.Output) {
	if m == nil || m.transport == nil {
		return
	}
	if err := m.transport.Display(ctx, output); err != nil {
		log.Printf("mobile: display source=%s session=%s: %v", output.Source, output.SessionID, err)
	}
	if output.Trace != nil && strings.EqualFold(output.Metadata["trace_actor"], "subagent") {
		return
	}
	text := mobiletransport.FinalText(output)
	if output.SessionID == "" || text == "" {
		return
	}
	jobID := output.Metadata["mobile_job_id"]
	if err := m.client.AppendTurn(ctx, output.SessionID, "model", "main", jobID, text); err != nil {
		log.Printf("mobile: mirror turn to D1 session=%s: %v", output.SessionID, err)
	}
}

func (m *mobileRuntime) mirrorUserTurn(input sdk.Input) func(context.Context) error {
	if m == nil || m.client == nil || input.SessionID == "" {
		return nil
	}
	text := inputText(input)
	if text == "" {
		return nil
	}
	return func(ctx context.Context) error {
		return m.client.AppendTurn(ctx, input.SessionID, "user", "user", "", text)
	}
}

func inputText(input sdk.Input) string {
	var b strings.Builder
	for _, part := range input.Turn.Content {
		b.WriteString(part.Text)
	}
	return strings.TrimSpace(b.String())
}

// sessionDBName mirrors sdk.SessionDBPath naming for the sync layer.
func sessionDBName(sessionID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(sessionID)) + ".db"
}

func listLocalSessionIDs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSuffix(entry.Name(), ".db"))
		if err != nil {
			continue
		}
		ids = append(ids, string(raw))
	}
	return ids, nil
}

// mobileSessionDir is where session databases live under the state root.
func mobileSessionDir(stateRoot string) string {
	return filepath.Join(stateRoot, "data", "sessions")
}
