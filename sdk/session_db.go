package sdk

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// SessionDB persists canonical session history plus every model request/response
// attempt. The database is the durable session backing store; the Session still
// keeps a synchronized in-memory copy for fast request construction.
type SessionDB struct {
	mu sync.Mutex
	db *sql.DB
}

func OpenSessionDB(path string) (*SessionDB, error) {
	if path == "" {
		return nil, errors.New("sdk: session database path is required")
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &SessionDB{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SessionDB) init() error {
	_, err := s.db.Exec(`
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;
PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  key_index INTEGER NOT NULL,
  thinking_level TEXT NOT NULL,
  temperature REAL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS turns (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  role TEXT NOT NULL,
  content_json TEXT NOT NULL,
  tool_call_json TEXT,
  tool_result_json TEXT,
  created_at TEXT NOT NULL,
  UNIQUE(session_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_turns_session_seq ON turns(session_id, seq);
CREATE TABLE IF NOT EXISTS requests (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  attempt INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  system_prompt TEXT NOT NULL,
  messages_json TEXT NOT NULL,
  tools_json TEXT NOT NULL,
  temperature REAL,
  thinking_level TEXT NOT NULL,
  max_output_tokens INTEGER NOT NULL,
  stream INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_requests_session_created ON requests(session_id, created_at);
CREATE TABLE IF NOT EXISTS responses (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  request_id INTEGER NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  success INTEGER NOT NULL,
  provider TEXT,
  model TEXT,
  content_json TEXT,
  tool_calls_json TEXT,
  finish_reason TEXT,
  usage_json TEXT,
  cache_json TEXT,
  error TEXT
);
CREATE INDEX IF NOT EXISTS idx_responses_request ON responses(request_id);
`)
	return err
}

func (s *SessionDB) Close() error {
	if s == nil || s.db == nil { return nil }
	s.mu.Lock(); defer s.mu.Unlock()
	return s.db.Close()
}

func (s *SessionDB) SaveSession(config SessionConfig) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	s.mu.Lock(); defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO sessions(id,provider,model,key_index,thinking_level,temperature,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET provider=excluded.provider,model=excluded.model,key_index=excluded.key_index,thinking_level=excluded.thinking_level,temperature=excluded.temperature,updated_at=excluded.updated_at`,
		config.ID, config.Provider, config.Model, config.KeyIndex, config.ThinkingLevel, nullableFloat(config.Temperature), now, now)
	return err
}

func nullableFloat(v *float64) any { if v == nil { return nil }; return *v }

func (s *SessionDB) LoadHistory(sessionID string) ([]Turn, error) {
	s.mu.Lock(); defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT role,content_json,tool_call_json,tool_result_json FROM turns WHERE session_id=? ORDER BY seq`, sessionID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []Turn
	for rows.Next() {
		var role, contentJSON string
		var callJSON, resultJSON sql.NullString
		if err := rows.Scan(&role, &contentJSON, &callJSON, &resultJSON); err != nil { return nil, err }
		var content []ContentPart
		if err := json.Unmarshal([]byte(contentJSON), &content); err != nil { return nil, err }
		turn := Turn{Role: Role(role), Content: content}
		if callJSON.Valid && callJSON.String != "" { var call ToolCall; if err := json.Unmarshal([]byte(callJSON.String), &call); err != nil { return nil, err }; turn.ToolCall = &call }
		if resultJSON.Valid && resultJSON.String != "" { var result ToolResult; if err := json.Unmarshal([]byte(resultJSON.String), &result); err != nil { return nil, err }; turn.ToolResult = &result }
		out = append(out, turn)
	}
	if err := rows.Err(); err != nil { return nil, err }
	return out, nil
}

func (s *SessionDB) AppendTurns(sessionID string, turns []Turn, startSeq int) error {
	if len(turns) == 0 { return nil }
	s.mu.Lock(); defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil { return err }
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO turns(session_id,seq,role,content_json,tool_call_json,tool_result_json,created_at) VALUES(?,?,?,?,?,?,?)`)
	if err != nil { return err }
	defer stmt.Close()
	for i, turn := range turns {
		content, err := json.Marshal(turn.Content); if err != nil { return err }
		var call any; if turn.ToolCall != nil { b, e := json.Marshal(turn.ToolCall); if e != nil { return e }; call = string(b) }
		var result any; if turn.ToolResult != nil { b, e := json.Marshal(turn.ToolResult); if e != nil { return e }; result = string(b) }
		if _, err := stmt.Exec(sessionID, startSeq+i, string(turn.Role), string(content), call, result, time.Now().UTC().Format(time.RFC3339Nano)); err != nil { return err }
	}
	return tx.Commit()
}

func (s *SessionDB) ReplaceTurns(sessionID string, turns []Turn) error {
	s.mu.Lock(); defer s.mu.Unlock()
	tx, err := s.db.Begin(); if err != nil { return err }; defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM turns WHERE session_id=?`, sessionID); err != nil { return err }
	stmt, err := tx.Prepare(`INSERT INTO turns(session_id,seq,role,content_json,tool_call_json,tool_result_json,created_at) VALUES(?,?,?,?,?,?,?)`)
	if err != nil { return err }; defer stmt.Close()
	for i, turn := range turns {
		content, err := json.Marshal(turn.Content); if err != nil { return err }
		var call any; if turn.ToolCall != nil { b, e := json.Marshal(turn.ToolCall); if e != nil { return e }; call = string(b) }
		var result any; if turn.ToolResult != nil { b, e := json.Marshal(turn.ToolResult); if e != nil { return e }; result = string(b) }
		if _, err := stmt.Exec(sessionID, i, string(turn.Role), string(content), call, result, time.Now().UTC().Format(time.RFC3339Nano)); err != nil { return err }
	}
	return tx.Commit()
}

func (s *SessionDB) RecordRequest(sessionID string, attempt int, req Request) (int64, error) {
	messages, err := json.Marshal(req.Messages); if err != nil { return 0, err }
	tools, err := json.Marshal(req.Tools); if err != nil { return 0, err }
	now := time.Now().UTC().Format(time.RFC3339Nano)
	s.mu.Lock(); defer s.mu.Unlock()
	res, err := s.db.Exec(`INSERT INTO requests(session_id,attempt,created_at,provider,model,system_prompt,messages_json,tools_json,temperature,thinking_level,max_output_tokens,stream) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, sessionID, attempt, now, req.Provider, req.Model, req.SystemPrompt, string(messages), string(tools), nullableFloat(req.Temperature), req.ThinkingLevel, req.MaxOutputTokens, boolInt(req.Stream))
	if err != nil { return 0, err }
	return res.LastInsertId()
}

func (s *SessionDB) RecordResponse(requestID int64, resp Response, err error) error {
	content, e := json.Marshal(resp.Content); if e != nil { return e }
	calls, e := json.Marshal(resp.ToolCalls); if e != nil { return e }
	usage, e := json.Marshal(resp.Usage); if e != nil { return e }
	cache, e := json.Marshal(resp.Cache); if e != nil { return e }
	var errorText any; if err != nil { errorText = err.Error() }
	now := time.Now().UTC().Format(time.RFC3339Nano)
	s.mu.Lock(); defer s.mu.Unlock()
	_, e = s.db.Exec(`INSERT INTO responses(request_id,created_at,success,provider,model,content_json,tool_calls_json,finish_reason,usage_json,cache_json,error) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, requestID, now, boolInt(err == nil), nullableString(resp.Provider), nullableString(resp.Model), string(content), string(calls), nullableString(resp.FinishReason), string(usage), string(cache), errorText)
	return e
}

func nullableString(v string) any { if v == "" { return nil }; return v }
func boolInt(v bool) int { if v { return 1 }; return 0 }

// RecordExchange persists one provider attempt, including failed attempts.
func (s *SessionDB) RecordExchange(sessionID string, attempt int, req Request, resp Response, err error) error {
	requestID, err := s.RecordRequest(sessionID, attempt, req)
	if err != nil { return err }
	return s.RecordResponse(requestID, resp, err)
}

var _ = context.Background
var _ = fmt.Sprintf
