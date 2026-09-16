package discord

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tulipskun/ai/sdk"
	"github.com/bwmarrin/discordgo"
)

// workspaceDiscord is the slice of the Discord API the /workspace handler
// needs. Tests inject a fake so no live Discord is ever required (REQ-038).
type workspaceDiscord interface {
	interactionAPI
}

// WorkspaceHandler implements /workspace: it selects the working directory
// for the invoking channel (applied to both the main and the sub session)
// with `~/` expansion, persisted on the sessions (REQ-038).
type WorkspaceHandler struct {
	ResolveSession    func(context.Context, sdk.Input) (*sdk.Session, error)
	SessionForChannel func(channelID string) string
}

const workspaceCommandName = "workspace"
const workspacePathOption = "path"

func (h *WorkspaceHandler) Handles(i *discordgo.InteractionCreate) bool {
	if h == nil || h.ResolveSession == nil || i == nil || i.Type != discordgo.InteractionApplicationCommand {
		return false
	}
	return i.ApplicationCommandData().Name == workspaceCommandName
}

func (h *WorkspaceHandler) Handle(s workspaceDiscord, i *discordgo.InteractionCreate) error {
	if !h.Handles(i) {
		return nil
	}
	data := i.ApplicationCommandData()
	raw := ""
	for _, option := range data.Options {
		if option != nil && option.Name == workspacePathOption {
			raw, _ = option.Value.(string)
		}
	}
	mainID := h.sessionIDFor(i.ChannelID)
	if strings.TrimSpace(raw) == "" {
		return h.replyStatus(s, i, mainID)
	}
	if err := deferEphemeralResponse(s, i); err != nil {
		return err
	}
	path, err := resolveWorkspacePath(raw)
	if err != nil {
		return followupEphemeral(s, i, "❌ "+err.Error())
	}
	mainSession, subSession, err := h.sessions(context.Background(), i, mainID)
	if err != nil {
		return followupEphemeral(s, i, "❌ ไม่สามารถโหลด session ได้: "+safeErrorSummary(err))
	}
	if err := mainSession.SetWorkspace(path); err != nil {
		return followupEphemeral(s, i, "❌ บันทึกล้มเหลว: "+safeErrorSummary(err))
	}
	if err := subSession.SetWorkspace(path); err != nil {
		return followupEphemeral(s, i, "❌ บันทึกฝั่ง sub ล้มเหลว: "+safeErrorSummary(err))
	}
	return followupEphemeral(s, i, fmt.Sprintf("✅ ทำงานที่ `%s` ทั้งฝั่ง main และ sub แล้ว", path))
}

func (h *WorkspaceHandler) replyStatus(s workspaceDiscord, i *discordgo.InteractionCreate, mainID string) error {
	session, err := h.resolve(context.Background(), mainID, i)
	if err != nil {
		return ephemeralRespond(s, i, "❌ ไม่สามารถโหลด session ได้: "+safeErrorSummary(err))
	}
	workspace := strings.TrimSpace(session.Config().Workspace)
	if workspace == "" {
		workspace = "(ค่าเริ่มต้นของระบบ — ยังไม่ได้เลือก)"
	}
	return ephemeralRespond(s, i, "📂 working directory: `"+workspace+"`\nใช้ `/workspace <path>` เพื่อเปลี่ยน (รองรับ `~/`)")
}

// resolveWorkspacePath expands `~` and `~/`, requires an absolute result,
// and accepts only existing directories.
func resolveWorkspacePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("ต้องระบุ path")
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, "~\\") {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return "", fmt.Errorf("ไม่พบ home directory ของระบบ")
		}
		rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(raw, "~"), "/"))
		rest = strings.TrimPrefix(rest, "\\")
		if rest == "" {
			raw = home
		} else {
			raw = filepath.Join(home, filepath.FromSlash(rest))
		}
	}
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("ต้องเป็น path สัมบูรณ์ (ขึ้นต้นด้วย `/` หรือ `~/`): %q", strings.TrimSpace(raw))
	}
	raw = filepath.Clean(raw)
	info, err := os.Stat(raw)
	if err != nil {
		return "", fmt.Errorf("ไม่พบตำแหน่งนี้: %s", raw)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("ไม่ใช่ directory: %s", raw)
	}
	return raw, nil
}

func (h *WorkspaceHandler) sessionIDFor(channelID string) string {
	channelID = strings.TrimSpace(channelID)
	if h != nil && h.SessionForChannel != nil {
		if id := strings.TrimSpace(h.SessionForChannel(channelID)); id != "" {
			return id
		}
	}
	return "discord:channel:" + channelID
}

func (h *WorkspaceHandler) resolve(ctx context.Context, sessionID string, i *discordgo.InteractionCreate) (*sdk.Session, error) {
	return h.ResolveSession(ctx, sdk.Input{Source: "discord", SessionID: sessionID, Metadata: map[string]string{"channel_id": i.ChannelID}})
}

func (h *WorkspaceHandler) sessions(ctx context.Context, i *discordgo.InteractionCreate, mainID string) (*sdk.Session, *sdk.Session, error) {
	mainSession, err := h.resolve(ctx, mainID, i)
	if err != nil {
		return nil, nil, err
	}
	subSession, err := h.resolve(ctx, mainID+":sub", i)
	if err != nil {
		return nil, nil, err
	}
	return mainSession, subSession, nil
}
