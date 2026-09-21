CREATE TABLE IF NOT EXISTS sessions (id TEXT PRIMARY KEY, config_json TEXT NOT NULL, history_json TEXT NOT NULL, usage_json TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS jobs (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, message TEXT NOT NULL, model TEXT NOT NULL, status TEXT NOT NULL, worker_id TEXT, cancel_requested INTEGER NOT NULL DEFAULT 0, lease_expires_at TEXT, error TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_jobs_queue ON jobs(status, cancel_requested, created_at);
CREATE TABLE IF NOT EXISTS job_events (id INTEGER PRIMARY KEY AUTOINCREMENT, job_id TEXT NOT NULL, type TEXT NOT NULL, payload_json TEXT NOT NULL, at_ms INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS idx_job_events_job_id ON job_events(job_id,id);
CREATE INDEX IF NOT EXISTS idx_sessions_updated_at ON sessions(updated_at);
