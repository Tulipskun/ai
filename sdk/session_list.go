package sdk

import (
	"time"
)

type SessionInfo struct {
	ID           string
	Provider     ProviderID
	Model        string
	UpdatedAt    time.Time
	TurnCount    int
}

func (s *SessionDB) ListSessions(limit int) ([]SessionInfo, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`
		SELECT s.id, s.provider, s.model, s.updated_at,
		       (SELECT COUNT(*) FROM turns t WHERE t.session_id = s.id)
		FROM sessions s
		ORDER BY s.updated_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionInfo
	for rows.Next() {
		var item SessionInfo
		var updated string
		if err := rows.Scan(&item.ID, &item.Provider, &item.Model, &updated, &item.TurnCount); err != nil {
			return nil, err
		}
		if parsed, err := time.Parse(time.RFC3339Nano, updated); err == nil {
			item.UpdatedAt = parsed
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
