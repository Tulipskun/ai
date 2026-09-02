package sdk

import (
	"errors"
	"testing"
)

func TestOpenSessionPersistsHistory(t *testing.T) {
	path := t.TempDir() + "/session.db"
	cfg := SessionConfig{ID: "s1", Provider: "test", Model: "model", KeyIndex: 0, ThinkingLevel: ThinkingNone}
	keys := NewKeyPool("key")
	s, err := OpenSession(path, cfg, keys); if err != nil { t.Fatal(err) }
	s.Append(Turn{Role: RoleUser, Content: []ContentPart{{Type: ContentText, Text: "hello"}}})
	s.Append(Turn{Role: RoleModel, Content: []ContentPart{{Type: ContentText, Text: "world"}}})
	if err := s.Close(); err != nil { t.Fatal(err) }

	reopened, err := OpenSession(path, cfg, keys); if err != nil { t.Fatal(err) }; defer reopened.Close()
	h := reopened.History(); if len(h) != 2 || h[0].Content[0].Text != "hello" || h[1].Content[0].Text != "world" { t.Fatalf("unexpected history: %#v", h) }
}

func TestSessionRollbackPersists(t *testing.T) {
	path := t.TempDir() + "/session.db"
	cfg := SessionConfig{ID: "s1", Provider: "test", Model: "model", KeyIndex: 0}
	s, err := OpenSession(path, cfg, NewKeyPool("key")); if err != nil { t.Fatal(err) }
	s.Append(Turn{Role: RoleUser, Content: []ContentPart{{Type: ContentText, Text: "keep"}}})
	s.Append(Turn{Role: RoleUser, Content: []ContentPart{{Type: ContentText, Text: "rollback"}}})
	s.ReplaceHistory(s.History()[:1]); if err := s.Close(); err != nil { t.Fatal(err) }
	reopened, err := OpenSession(path, cfg, NewKeyPool("key")); if err != nil { t.Fatal(err) }; defer reopened.Close()
	if h := reopened.History(); len(h) != 1 || h[0].Content[0].Text != "keep" { t.Fatalf("rollback was not persisted: %#v", h) }
}

func TestSessionDBRecordsFailedAttempt(t *testing.T) {
	path := t.TempDir() + "/session.db"
	cfg := SessionConfig{ID: "s1", Provider: "test", Model: "model", KeyIndex: 0}
	s, err := OpenSession(path, cfg, NewKeyPool("key")); if err != nil { t.Fatal(err) }
	req := Request{Provider: "test", Model: "model", SystemPrompt: "system", Messages: []Turn{{Role: RoleUser}}, ThinkingLevel: ThinkingLow, MaxOutputTokens: 100}
	id, err := s.RecordRequest(1, req); if err != nil { t.Fatal(err) }
	wantErr := errors.New("provider failed")
	if err := s.RecordResponse(id, Response{}, wantErr); err != nil { t.Fatal(err) }
	if err := s.Close(); err != nil { t.Fatal(err) }

	db, err := OpenSessionDB(path); if err != nil { t.Fatal(err) }; defer db.Close()
	var success int; var recorded string
	if err := db.db.QueryRow(`SELECT success,error FROM responses WHERE request_id=?`, id).Scan(&success, &recorded); err != nil { t.Fatal(err) }
	if success != 0 || recorded != wantErr.Error() { t.Fatalf("unexpected response record: success=%d error=%q", success, recorded) }
}
