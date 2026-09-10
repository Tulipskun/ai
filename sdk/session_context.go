package sdk

import "context"

type sessionContextKey struct{}

func WithSessionID(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionContextKey{}, sessionID)
}

func SessionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(sessionContextKey{}).(string)
	return value
}
