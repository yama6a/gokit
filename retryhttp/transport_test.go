package retryhttp_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yama6a/gokit/retryhttp"
)

var epoch = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

type recorder struct {
	mu     sync.Mutex
	waits  []time.Duration
	calls  int
	bodies []string
}

func (r *recorder) sleep(_ context.Context, d time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.waits = append(r.waits, d)
	return nil
}

// serve answers call n with statuses[n], repeating the last status.
func (r *recorder) serve(t *testing.T, statuses []int, header http.Header) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		i := min(r.calls, len(statuses)-1)
		r.calls++
		r.bodies = append(r.bodies, string(body))
		r.mu.Unlock()
		for k, v := range header {
			w.Header()[k] = v
		}
		w.WriteHeader(statuses[i])
		_, _ = w.Write([]byte("status " + strconv.Itoa(statuses[i])))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (r *recorder) transport() *retryhttp.Transport {
	return retryhttp.New(nil,
		retryhttp.WithSleep(r.sleep),
		retryhttp.WithJitter(func() float64 { return 0.5 }),
		retryhttp.WithNow(func() time.Time { return epoch }),
	)
}

func do(ctx context.Context, t *testing.T, rt http.RoundTripper, method, url string, body io.Reader) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	require.NoError(t, err)
	return rt.RoundTrip(req) //nolint:wrapcheck // test helper
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return string(b)
}

func TestTransport_Attempts(t *testing.T) {
	t.Parallel()

	const (
		ms = time.Millisecond
		s  = time.Second
	)
	tests := []struct {
		name      string
		policy    *retryhttp.Policy
		statuses  []int
		header    http.Header
		wantCalls int
		wantCode  int
		wantWaits []time.Duration
	}{
		{
			name:      "no policy means one attempt",
			statuses:  []int{503},
			wantCalls: 1,
			wantCode:  503,
		},
		{
			name:      "one attempt policy",
			policy:    &retryhttp.Policy{Attempts: 1, Base: 100 * ms},
			statuses:  []int{503},
			wantCalls: 1,
			wantCode:  503,
		},
		{
			name:      "success first try",
			policy:    &retryhttp.Policy{Attempts: 3, Base: 100 * ms},
			statuses:  []int{200},
			wantCalls: 1,
			wantCode:  200,
		},
		{
			name:      "recovers after 5xx with doubling backoff",
			policy:    &retryhttp.Policy{Attempts: 4, Base: 100 * ms},
			statuses:  []int{500, 503, 200},
			wantCalls: 3,
			wantCode:  200,
			wantWaits: []time.Duration{50 * ms, 100 * ms},
		},
		{
			name:      "recovers after 429",
			policy:    &retryhttp.Policy{Attempts: 2, Base: 100 * ms},
			statuses:  []int{429, 200},
			wantCalls: 2,
			wantCode:  200,
			wantWaits: []time.Duration{50 * ms},
		},
		{
			name:      "exhausted returns last response",
			policy:    &retryhttp.Policy{Attempts: 3, Base: 100 * ms},
			statuses:  []int{500, 502, 503, 200},
			wantCalls: 3,
			wantCode:  503,
			wantWaits: []time.Duration{50 * ms, 100 * ms},
		},
		{
			name:      "4xx is not retried",
			policy:    &retryhttp.Policy{Attempts: 3, Base: 100 * ms},
			statuses:  []int{404, 200},
			wantCalls: 1,
			wantCode:  404,
		},
		{
			name:      "backoff is capped at Max",
			policy:    &retryhttp.Policy{Attempts: 5, Base: 100 * ms, Max: 150 * ms},
			statuses:  []int{500},
			wantCalls: 5,
			wantCode:  500,
			wantWaits: []time.Duration{50 * ms, 75 * ms, 75 * ms, 75 * ms},
		},
		{
			name:      "custom RetryOn retries a 404",
			policy:    &retryhttp.Policy{Attempts: 2, Base: 100 * ms, RetryOn: func(r *http.Response, _ error) bool { return r.StatusCode == http.StatusNotFound }},
			statuses:  []int{404, 200},
			wantCalls: 2,
			wantCode:  200,
			wantWaits: []time.Duration{50 * ms},
		},
		{
			name:      "custom RetryOn declines a 500",
			policy:    &retryhttp.Policy{Attempts: 3, Base: 100 * ms, RetryOn: func(*http.Response, error) bool { return false }},
			statuses:  []int{500, 200},
			wantCalls: 1,
			wantCode:  500,
		},
		{
			name:      "Retry-After seconds replaces backoff",
			policy:    &retryhttp.Policy{Attempts: 2, Base: 100 * ms, Max: time.Minute},
			statuses:  []int{429, 200},
			header:    http.Header{"Retry-After": {"2"}},
			wantCalls: 2,
			wantCode:  200,
			wantWaits: []time.Duration{2 * s},
		},
		{
			name:      "Retry-After seconds is capped at Max",
			policy:    &retryhttp.Policy{Attempts: 2, Base: 100 * ms, Max: s},
			statuses:  []int{503, 200},
			header:    http.Header{"Retry-After": {"120"}},
			wantCalls: 2,
			wantCode:  200,
			wantWaits: []time.Duration{s},
		},
		{
			name:      "Retry-After without Max is not capped",
			policy:    &retryhttp.Policy{Attempts: 2, Base: 100 * ms},
			statuses:  []int{503, 200},
			header:    http.Header{"Retry-After": {"120"}},
			wantCalls: 2,
			wantCode:  200,
			wantWaits: []time.Duration{120 * s},
		},
		{
			name:      "Retry-After HTTP-date",
			policy:    &retryhttp.Policy{Attempts: 2, Base: 100 * ms, Max: time.Minute},
			statuses:  []int{503, 200},
			header:    http.Header{"Retry-After": {epoch.Add(30 * s).Format(http.TimeFormat)}},
			wantCalls: 2,
			wantCode:  200,
			wantWaits: []time.Duration{30 * s},
		},
		{
			name:      "Retry-After HTTP-date is capped at Max",
			policy:    &retryhttp.Policy{Attempts: 2, Base: 100 * ms, Max: 10 * s},
			statuses:  []int{503, 200},
			header:    http.Header{"Retry-After": {epoch.Add(30 * s).Format(http.TimeFormat)}},
			wantCalls: 2,
			wantCode:  200,
			wantWaits: []time.Duration{10 * s},
		},
		{
			name:      "Retry-After HTTP-date in the past waits zero",
			policy:    &retryhttp.Policy{Attempts: 2, Base: 100 * ms, Max: time.Minute},
			statuses:  []int{503, 200},
			header:    http.Header{"Retry-After": {epoch.Add(-time.Hour).Format(http.TimeFormat)}},
			wantCalls: 2,
			wantCode:  200,
			wantWaits: []time.Duration{0},
		},
		{
			name:      "invalid Retry-After falls back to backoff",
			policy:    &retryhttp.Policy{Attempts: 2, Base: 100 * ms},
			statuses:  []int{503, 200},
			header:    http.Header{"Retry-After": {"soon"}},
			wantCalls: 2,
			wantCode:  200,
			wantWaits: []time.Duration{50 * ms},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := &recorder{}
			srv := rec.serve(t, tt.statuses, tt.header)
			ctx := context.Background()
			if tt.policy != nil {
				ctx = retryhttp.WithPolicy(ctx, *tt.policy)
			}

			resp, err := do(ctx, t, rec.transport(), http.MethodGet, srv.URL, nil)
			require.NoError(t, err)

			require.Equal(t, tt.wantCode, resp.StatusCode)
			require.Equal(t, "status "+strconv.Itoa(tt.wantCode), readBody(t, resp))
			require.Equal(t, tt.wantCalls, rec.calls)
			require.Equal(t, tt.wantWaits, rec.waits)
		})
	}
}

func TestTransport_RewindsBody(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	srv := rec.serve(t, []int{500, 502, 200}, nil)
	ctx := retryhttp.WithPolicy(context.Background(), retryhttp.Policy{Attempts: 3, Base: time.Millisecond})

	resp, err := do(ctx, t, rec.transport(), http.MethodPost, srv.URL, bytes.NewReader([]byte("payload")))
	require.NoError(t, err)

	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, []string{"payload", "payload", "payload"}, rec.bodies)
	require.NoError(t, resp.Body.Close())
}

func TestTransport_BodyWithoutGetBodyGetsOneAttempt(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	srv := rec.serve(t, []int{500, 200}, nil)
	ctx := retryhttp.WithPolicy(context.Background(), retryhttp.Policy{Attempts: 3, Base: time.Millisecond})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, io.NopCloser(bytes.NewReader([]byte("payload"))))
	require.NoError(t, err)
	require.Nil(t, req.GetBody)

	resp, err := rec.transport().RoundTrip(req)
	require.NoError(t, err)

	require.Equal(t, 500, resp.StatusCode)
	require.Equal(t, []string{"payload"}, rec.bodies)
	require.Empty(t, rec.waits)
	require.NoError(t, resp.Body.Close())
}

func TestTransport_GetBodyFails(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	srv := rec.serve(t, []int{500, 200}, nil)
	ctx := retryhttp.WithPolicy(context.Background(), retryhttp.Policy{Attempts: 3, Base: time.Millisecond})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, bytes.NewReader([]byte("payload")))
	require.NoError(t, err)
	errRewind := errors.New("cannot rewind")
	req.GetBody = func() (io.ReadCloser, error) { return nil, errRewind }

	_, err = rec.transport().RoundTrip(req) //nolint:bodyclose // the response is nil when the rewind fails

	require.ErrorIs(t, err, errRewind)
	require.Equal(t, 1, rec.calls)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTransport_ConnectionErrors(t *testing.T) {
	t.Parallel()

	errConn := errors.New("connection refused")
	tests := []struct {
		name      string
		errs      []error
		wantCalls int
		wantErr   error
		wantCode  int
	}{
		{name: "retried until success", errs: []error{errConn, errConn, nil}, wantCalls: 3, wantCode: 200},
		{name: "exhausted returns last error", errs: []error{errConn}, wantCalls: 3, wantErr: errConn},
		{name: "canceled is not retried", errs: []error{context.Canceled, nil}, wantCalls: 1, wantErr: context.Canceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				err := tt.errs[min(calls, len(tt.errs)-1)]
				calls++
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: r}, nil
			})
			rec := &recorder{}
			rt := retryhttp.New(base, retryhttp.WithSleep(rec.sleep), retryhttp.WithJitter(func() float64 { return 0 }))
			ctx := retryhttp.WithPolicy(context.Background(), retryhttp.Policy{Attempts: 3, Base: time.Millisecond})

			resp, err := do(ctx, t, rt, http.MethodGet, "http://example.invalid", nil)

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.wantCalls, calls)
			if tt.wantErr != nil {
				require.Nil(t, resp)
				return
			}
			require.Equal(t, tt.wantCode, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
		})
	}
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error {
	b.closed = true
	return nil
}

func TestTransport_DrainsAndClosesRetriedResponses(t *testing.T) {
	t.Parallel()

	var bodies []*trackedBody
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := 503
		if len(bodies) == 2 {
			status = 200
		}
		b := &trackedBody{Reader: bytes.NewReader([]byte("unread"))}
		bodies = append(bodies, b)
		return &http.Response{StatusCode: status, Body: b, Request: r}, nil
	})
	rec := &recorder{}
	rt := retryhttp.New(base, retryhttp.WithSleep(rec.sleep))
	ctx := retryhttp.WithPolicy(context.Background(), retryhttp.Policy{Attempts: 3, Base: time.Millisecond})

	resp, err := do(ctx, t, rt, http.MethodGet, "http://example.invalid", nil)
	require.NoError(t, err)

	require.Equal(t, 200, resp.StatusCode)
	require.Len(t, bodies, 3)
	for _, b := range bodies[:2] {
		require.True(t, b.closed)
		rest, _ := io.ReadAll(b)
		require.Empty(t, rest)
	}
	require.False(t, bodies[2].closed)
	require.NoError(t, resp.Body.Close())
}

func TestTransport_ContextEndsDuringWait(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	srv := rec.serve(t, []int{503, 200}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = retryhttp.WithPolicy(ctx, retryhttp.Policy{Attempts: 3, Base: time.Hour})
	rt := retryhttp.New(nil,
		retryhttp.WithSleep(func(ctx context.Context, _ time.Duration) error {
			cancel()
			return ctx.Err()
		}),
	)

	_, err := do(ctx, t, rt, http.MethodGet, srv.URL, nil) //nolint:bodyclose // the response is nil when the wait fails

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, rec.calls)
}

func TestTransport_ContextAlreadyEndedStopsRetries(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = retryhttp.WithPolicy(ctx, retryhttp.Policy{
		Attempts: 3,
		Base:     time.Millisecond,
		RetryOn:  func(*http.Response, error) bool { return true },
	})
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		cancel()
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody, Request: r}, nil
	})

	resp, err := do(ctx, t, retryhttp.New(base, retryhttp.WithSleep(rec.sleep)), http.MethodGet, "http://example.invalid", nil)
	require.NoError(t, err)

	require.Equal(t, 503, resp.StatusCode)
	require.Empty(t, rec.waits)
	require.NoError(t, resp.Body.Close())
}

func TestTransport_Defaults(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	srv := rec.serve(t, []int{503, 503, 200}, nil)
	ctx := retryhttp.WithPolicy(context.Background(), retryhttp.Policy{Attempts: 3, Base: time.Millisecond, Max: 2 * time.Millisecond})
	client := &http.Client{Transport: retryhttp.New(nil)}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, 3, rec.calls)
	require.NoError(t, resp.Body.Close())
}

func TestTransport_DefaultClockForRetryAfterDate(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	past := time.Now().Add(-time.Hour).Format(http.TimeFormat)
	srv := rec.serve(t, []int{503, 200}, http.Header{"Retry-After": {past}})
	ctx := retryhttp.WithPolicy(context.Background(), retryhttp.Policy{Attempts: 2, Base: time.Hour})

	resp, err := do(ctx, t, retryhttp.New(nil), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, 2, rec.calls)
	require.NoError(t, resp.Body.Close())
}
