package retryhttp

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Policy tells the Transport how to retry one request.
// Attempts counts all tries, so 1 means no retry.
// Base is the first backoff ceiling and doubles per attempt up to Max.
// RetryOn defaults to DefaultRetryOn when nil.
type Policy struct {
	Attempts int
	Base     time.Duration
	Max      time.Duration
	// Delays, when set, replaces the jittered backoff: retry n waits Delays[n-1], the last
	// entry repeats, and Attempts 0 means len(Delays)+1.
	Delays  []time.Duration
	RetryOn func(*http.Response, error) bool
}

func (p Policy) attempts() int {
	if p.Attempts == 0 && len(p.Delays) > 0 {
		return len(p.Delays) + 1
	}
	return p.Attempts
}

type policyKey struct{}

// WithPolicy returns a context that makes the Transport apply p to requests that carry it.
func WithPolicy(ctx context.Context, p Policy) context.Context {
	return context.WithValue(ctx, policyKey{}, p)
}

// PolicyFrom returns the policy set by WithPolicy.
func PolicyFrom(ctx context.Context) (Policy, bool) {
	p, ok := ctx.Value(policyKey{}).(Policy)
	return p, ok
}

// DefaultRetryOn retries 5xx, 429 and transport errors, except when the context ended.
func DefaultRetryOn(resp *http.Response, err error) bool {
	if err != nil {
		return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
	}
	return resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests
}
