package sdk

type WorkerSessionOpener interface {
	OpenWorkerSession(SessionConfig, *KeyPool) (*Session, error)
}

type SessionStore interface {
	SaveSession(SessionConfig) error
	LoadSession(string) (SessionConfig, error)
	LoadUsage(string) (Usage, error)
	LoadHistory(string) ([]Turn, error)
	AppendTurns(string, []Turn, int) error
	ReplaceTurns(string, []Turn) error
	RecordRequest(string, int, Request) (int64, error)
	RecordResponse(int64, Response, error) error
	Close() error
}
