package sdk

import (
	"errors"
	"time"
)

// BindDiscordChannel associates a Discord channel with a session.
// The same channel can be bound to only one session.
func (s *SessionDB) BindDiscordChannel(sessionID, channelID string) error {
	if s == nil || s.db == nil {
		return errors.New("sdk: session database is nil")
	}
	if sessionID == "" {
		return errors.New("sdk: session ID is required")
	}
	if channelID == "" {
		return errors.New("sdk: Discord channel ID is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO session_discord_channels(session_id, discord_channel_id, created_at)
		 VALUES(?,?,?)
		 ON CONFLICT(session_id, discord_channel_id) DO NOTHING`,
		sessionID, channelID, time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

// UnbindDiscordChannel removes a Discord channel from a session.
func (s *SessionDB) UnbindDiscordChannel(sessionID, channelID string) error {
	if s == nil || s.db == nil {
		return errors.New("sdk: session database is nil")
	}
	if sessionID == "" || channelID == "" {
		return errors.New("sdk: session ID and Discord channel ID are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`DELETE FROM session_discord_channels WHERE session_id=? AND discord_channel_id=?`,
		sessionID, channelID,
	)
	return err
}

// DiscordChannels returns all Discord channels bound to a session.
func (s *SessionDB) DiscordChannels(sessionID string) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sdk: session database is nil")
	}
	if sessionID == "" {
		return nil, errors.New("sdk: session ID is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(
		`SELECT discord_channel_id FROM session_discord_channels
		 WHERE session_id=? ORDER BY created_at, discord_channel_id`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var channelID string
		if err := rows.Scan(&channelID); err != nil {
			return nil, err
		}
		out = append(out, channelID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SessionIDByDiscordChannel returns the session bound to a Discord channel.
func (s *SessionDB) SessionIDByDiscordChannel(channelID string) (string, error) {
	if s == nil || s.db == nil {
		return "", errors.New("sdk: session database is nil")
	}
	if channelID == "" {
		return "", errors.New("sdk: Discord channel ID is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var sessionID string
	err := s.db.QueryRow(
		`SELECT session_id FROM session_discord_channels WHERE discord_channel_id=?`,
		channelID,
	).Scan(&sessionID)
	return sessionID, err
}
