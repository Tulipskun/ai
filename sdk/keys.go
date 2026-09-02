package sdk

import (
    "errors"
    "net/http"
    "strings"
    "sync"
)

// KeyPool rotates API keys sequentially. A key is advanced after a retryable
// authentication/rate-limit failure, allowing the next request to use the next key.
type KeyPool struct {
    mu      sync.Mutex
    keys    []string
    current int
}

func NewKeyPool(keys ...string) *KeyPool {
    cleaned := make([]string, 0, len(keys))
    for _, k := range keys {
        if k = strings.TrimSpace(k); k != "" {
            cleaned = append(cleaned, k)
        }
    }
    return &KeyPool{keys: cleaned}
}

func (p *KeyPool) Current() (string, error) {
    p.mu.Lock()
    defer p.mu.Unlock()
    if len(p.keys) == 0 { return "", errors.New("sdk: no API keys configured") }
    return p.keys[p.current%len(p.keys)], nil
}

func (p *KeyPool) Rotate() (string, error) {
    p.mu.Lock()
    defer p.mu.Unlock()
    if len(p.keys) == 0 { return "", errors.New("sdk: no API keys configured") }
    p.current = (p.current + 1) % len(p.keys)
    return p.keys[p.current], nil
}

func RetryableHTTPStatus(status int) bool {
    return status == http.StatusTooManyRequests || status == http.StatusUnauthorized || status == http.StatusForbidden
}
