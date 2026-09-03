package sdk

import "testing"

func TestSessionConfigAllowsUnsetModel(t *testing.T) {
	session, err := NewSession(SessionConfig{ID: "test-session"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := session.Config().Model; got != "" {
		t.Fatalf("expected unset model, got %q", got)
	}
}
