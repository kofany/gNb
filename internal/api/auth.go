package api

import (
	"crypto/subtle"
	"sync"
	"time"
)

// tokenChecker does constant-time comparison and per-IP rate limiting.
type tokenChecker struct {
	expected []byte

	mu      sync.Mutex
	fails   map[string][]time.Time
	window  time.Duration
	maxFail int
}

func newTokenChecker(expected string) *tokenChecker {
	return &tokenChecker{
		expected: []byte(expected),
		fails:    make(map[string][]time.Time),
		window:   60 * time.Second,
		maxFail:  5,
	}
}

// CheckToken reports ok=true if token matches. rateLimited=true means the
// caller exceeded the attempt limit and is temporarily blocked.
func (c *tokenChecker) CheckToken(remote, token string) (ok, rateLimited bool) {
	c.mu.Lock()
	now := time.Now()
	times := c.fails[remote]
	cutoff := now.Add(-c.window)
	pruned := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	if len(pruned) >= c.maxFail {
		c.fails[remote] = pruned
		c.mu.Unlock()
		return false, true
	}
	c.mu.Unlock()

	if subtle.ConstantTimeCompare([]byte(token), c.expected) == 1 {
		c.mu.Lock()
		delete(c.fails, remote)
		c.mu.Unlock()
		return true, false
	}

	c.mu.Lock()
	c.fails[remote] = append(pruned, now)
	c.mu.Unlock()
	return false, false
}
