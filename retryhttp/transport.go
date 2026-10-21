// Package retryhttp retries HTTP requests with exponential backoff, chosen per request through the context.
package retryhttp

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

const drainLimit = 64 << 10

// Transport is an http.RoundTripper that retries according to the Policy on the request context.
type Transport struct {
	base   http.RoundTripper
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error
	jitter func() float64
}

var _ http.RoundTripper = (*Transport)(nil)

// Option configures a Transport.
type Option func(*Transport)

// WithNow sets the clock used to read HTTP-date values of Retry-After.
func WithNow(now func() time.Time) Option {
	return func(t *Transport) { t.now = now }
}

// WithSleep sets the wait function. It must return the context error when ctx ends first.
func WithSleep(sleep func(context.Context, time.Duration) error) Option {
	return func(t *Transport) { t.sleep = sleep }
}

// WithJitter sets the source of the backoff scale. It returns a value in [0, 1).
func WithJitter(jitter func() float64) Option {
	return func(t *Transport) { t.jitter = jitter }
}

// New wraps base, or http.DefaultTransport when base is nil.
func New(base http.RoundTripper, opts ...Option) *Transport {
	if base == nil {
		base = http.DefaultTransport
	}
	t := &Transport{base: base, now: time.Now, sleep: sleep, jitter: rand.Float64}
	for _, o := range opts {
		o(t)
	}
	return t
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	p, ok := policyFor(req)
	if !ok {
		return t.base.RoundTrip(req) //nolint:wrapcheck // a RoundTripper passes transport errors through unchanged
	}
	retryOn := p.RetryOn
	if retryOn == nil {
		retryOn = DefaultRetryOn
	}

	ctx := req.Context()
	for attempt := 1; ; attempt++ {
		resp, err := t.base.RoundTrip(req)
		if attempt >= p.Attempts || ctx.Err() != nil || !retryOn(resp, err) {
			return resp, err //nolint:wrapcheck // a RoundTripper passes transport errors through unchanged
		}
		wait := t.backoff(p, attempt, resp)
		if resp != nil {
			_, _ = io.CopyN(io.Discard, resp.Body, drainLimit)
			_ = resp.Body.Close()
		}
		if err := t.sleep(ctx, wait); err != nil {
			return nil, fmt.Errorf("wait before retry: %w", err)
		}
		if req, err = rewind(req); err != nil {
			return nil, err
		}
	}
}

// policyFor reports whether req may be tried more than once. A body that cannot be replayed allows one attempt.
func policyFor(req *http.Request) (Policy, bool) {
	p, ok := PolicyFrom(req.Context())
	if !ok || p.Attempts <= 1 {
		return Policy{}, false
	}
	if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
		return Policy{}, false
	}
	return p, true
}

func rewind(req *http.Request) (*http.Request, error) {
	if req.GetBody == nil {
		return req, nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("rewind request body: %w", err)
	}
	next := req.Clone(req.Context())
	next.Body = body
	return next, nil
}

func (t *Transport) backoff(p Policy, attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if d, ok := t.retryAfter(resp.Header.Get("Retry-After")); ok {
			if p.Max > 0 {
				d = min(d, p.Max)
			}
			return d
		}
	}
	ceiling := p.Base
	for i := 1; i < attempt && (p.Max <= 0 || ceiling < p.Max); i++ {
		ceiling *= 2
	}
	if p.Max > 0 {
		ceiling = min(ceiling, p.Max)
	}
	return time.Duration(t.jitter() * float64(ceiling))
}

func (t *Transport) retryAfter(v string) (time.Duration, bool) {
	if secs, err := strconv.ParseUint(v, 10, 32); err == nil {
		return time.Duration(secs) * time.Second, true
	}
	if at, err := http.ParseTime(v); err == nil {
		return max(0, at.Sub(t.now())), true
	}
	return 0, false
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck // RoundTrip adds the context
	case <-timer.C:
		return nil
	}
}
