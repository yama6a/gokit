// Package backoff holds the exponential backoff that retryhttp and pgretry share.
package backoff

import (
	"context"
	"time"
)

// Ceiling returns base doubled once per attempt after the first, capped at limit when limit > 0.
// Callers scale it by a jitter in [0, 1).
func Ceiling(base, limit time.Duration, attempt int) time.Duration {
	ceiling := base
	for i := 1; i < attempt && (limit <= 0 || ceiling < limit); i++ {
		ceiling *= 2
	}
	if limit > 0 {
		ceiling = min(ceiling, limit)
	}
	return ceiling
}

// Sleep waits for d and returns the context error when ctx ends first.
func Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck // the caller adds the context
	case <-timer.C:
		return nil
	}
}
