package discord

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tulipskun/ai/sdk"
	"github.com/bwmarrin/discordgo"
)

func workspaceTestSessions(t *testing.T) (*sdk.Session, *sdk.Session, func(context.Context, sdk.Input) (*sdk.Session, error)) {
	t.Helper()
	keys := sdk.NewKeyPool("k")
	main := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1"}, keys)
	sub := sdk.NewSession(sdk.SessionConfig{ID: "discord:channel:c1:sub"}, keys)
	all := map[string]*sdk.Session{main.ID(): main, sub.ID(): sub}
	return main, sub, func(_ context.Context, input sdk.Input) (*sdk.Session, error) {
		session, ok := all[input.SessionID]
		if !ok {
			return nil, os.ErrNotExist
		}
		return session, nil
	}
}

func workspaceInteraction(path *string) *discordgo.InteractionCreate {
	options := []*discordgo.ApplicationCommandInteractionDataOption{}
	if path != nil {
		options = append(options, &discordgo.ApplicationCommandInteractionDataOption{Name: "path", Type: discordgo.ApplicationCommandOptionString, Value: *path})
	}
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      discordgo.InteractionApplicationCommand,
		ID:        "interaction-workspace",
		Token:     "token",
		ChannelID: "c1",
		Data: discordgo.ApplicationCommandInteractionData{
			ID:      "cmd-workspace",
			Name:    "workspace",
			Options: options,
		},
	}}
}

func TestWorkspaceCommandSetsBothSessions(t *testing.T) {
	main, sub, resolve := workspaceTestSessions(t)
	workspaceDir := t.TempDir()
	handler := &WorkspaceHandler{ResolveSession: resolve, SessionForChannel: func(string) string { return main.ID() }}
	fake := &fakeInteractionAPI{}
	path := workspaceDir
	if err := handler.Handle(fake, workspaceInteraction(&path)); err != nil {
		t.Fatal(err)
	}
	if main.Config().Workspace != workspaceDir || sub.Config().Workspace != workspaceDir {
		t.Fatalf("sessions not updated: %q %q", main.Config().Workspace, sub.Config().Workspace)
	}
	if len(fake.followups) != 1 || !strings.Contains(fake.followups[0].Content, workspaceDir) {
		t.Fatalf("missing confirmation: %+v", fake.followups)
	}
}

func TestWorkspaceCommandExpandsHome(t *testing.T) {
	_, _, resolve := workspaceTestSessions(t)
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	handler := &WorkspaceHandler{ResolveSession: resolve, SessionForChannel: func(string) string { return "discord:channel:c1" }}
	fake := &fakeInteractionAPI{}
	path := "~/nonexistent-subdir-xyz"
	if err := handler.Handle(fake, workspaceInteraction(&path)); err != nil {
		t.Fatal(err)
	}
	if len(fake.followups) != 1 || !strings.Contains(fake.followups[0].Content, filepath.Join(home, "nonexistent-subdir-xyz")) {
		t.Fatalf("~/ expansion missing from rejection: %+v", fake.followups)
	}
}

func TestWorkspaceCommandRejectsMissingDirectory(t *testing.T) {
	main, _, resolve := workspaceTestSessions(t)
	handler := &WorkspaceHandler{ResolveSession: resolve, SessionForChannel: func(string) string { return main.ID() }}
	fake := &fakeInteractionAPI{}
	path := filepath.Join(t.TempDir(), "missing")
	if err := handler.Handle(fake, workspaceInteraction(&path)); err != nil {
		t.Fatal(err)
	}
	if main.Config().Workspace != "" {
		t.Fatal("missing directory stored anyway")
	}
	if len(fake.followups) != 1 || !strings.Contains(fake.followups[0].Content, "ไม่พบตำแหน่งนี้") {
		t.Fatalf("missing rejection message: %+v", fake.followups)
	}
}

func TestWorkspaceCommandStatusWhenNoPath(t *testing.T) {
	main, _, resolve := workspaceTestSessions(t)
	work := t.TempDir()
	if err := main.SetWorkspace(work); err != nil {
		t.Fatal(err)
	}
	handler := &WorkspaceHandler{ResolveSession: resolve, SessionForChannel: func(string) string { return main.ID() }}
	fake := &fakeInteractionAPI{}
	if err := handler.Handle(fake, workspaceInteraction(nil)); err != nil {
		t.Fatal(err)
	}
	if len(fake.responds) != 1 || fake.responds[0].Data == nil || !strings.Contains(fake.responds[0].Data.Content, work) {
		t.Fatalf("status response wrong: %+v", fake.responds)
	}
}

func TestWorkspaceResolvePathRejectsRelative(t *testing.T) {
	if _, err := resolveWorkspacePath("some/relative"); err == nil {
		t.Fatal("relative path accepted")
	}
	if _, err := resolveWorkspacePath("  "); err == nil {
		t.Fatal("empty path accepted")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveWorkspacePath(filepath.Join(dir, "file.txt")); err == nil {
		t.Fatal("non-directory accepted")
	}
}

func TestWorkspaceCommandIgnoresOtherInteractions(t *testing.T) {
	_, _, resolve := workspaceTestSessions(t)
	handler := &WorkspaceHandler{ResolveSession: resolve, SessionForChannel: func(string) string { return "x" }}
	fake := &fakeInteractionAPI{}
	other := workspaceInteraction(nil)
	other.Interaction.Data = discordgo.ApplicationCommandInteractionData{ID: "other", Name: "model"}
	if err := handler.Handle(fake, other); err != nil {
		t.Fatal(err)
	}
	if len(fake.responds)+len(fake.followups) != 0 {
		t.Fatal("handler answered an unrelated command")
	}
}
