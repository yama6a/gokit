package httpserver_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/yama6a/gokit/httpserver"
)

func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest(t, method, path))

	return rec
}

func newRequest(t *testing.T, method, path string) *http.Request {
	t.Helper()

	return httptest.NewRequestWithContext(t.Context(), method, path, nil)
}

func get(t *testing.T, url string) (*http.Response, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}

	return resp, nil
}

func TestServer_Handler(t *testing.T) {
	t.Parallel()

	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "api")
	})
	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("metrics"))
	})
	errNotReady := errors.New("db down")

	tests := []struct {
		name       string
		opts       httpserver.Options
		method     string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "healthz", opts: httpserver.Options{}, method: "GET", path: "/healthz", wantStatus: 200, wantBody: "ok"},
		{name: "healthz non-GET falls through to API", opts: httpserver.Options{API: api}, method: "POST", path: "/healthz", wantStatus: 200, wantBody: "api"},
		{name: "readyz without check", opts: httpserver.Options{}, method: "GET", path: "/readyz", wantStatus: 200, wantBody: "ok"},
		{
			name:       "readyz ready",
			opts:       httpserver.Options{Ready: func(context.Context) error { return nil }},
			method:     "GET",
			path:       "/readyz",
			wantStatus: 200,
			wantBody:   "ok",
		},
		{
			name:       "readyz not ready",
			opts:       httpserver.Options{Ready: func(context.Context) error { return errNotReady }},
			method:     "GET",
			path:       "/readyz",
			wantStatus: 503,
			wantBody:   "not ready\n",
		},
		{name: "metrics", opts: httpserver.Options{Metrics: metrics, API: api}, method: "GET", path: "/metrics", wantStatus: 200, wantBody: "metrics"},
		{name: "metrics nil", opts: httpserver.Options{API: api}, method: "GET", path: "/metrics", wantStatus: 404, wantBody: "404 page not found\n"},
		{name: "api", opts: httpserver.Options{API: api}, method: "GET", path: "/v1/things", wantStatus: 200, wantBody: "api"},
		{name: "api nil", opts: httpserver.Options{}, method: "GET", path: "/v1/things", wantStatus: 404, wantBody: "404 page not found\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := do(t, httpserver.New(tt.opts).Handler(), tt.method, tt.path)

			require.Equal(t, tt.wantStatus, rec.Code)
			require.Equal(t, tt.wantBody, rec.Body.String())
		})
	}
}

func TestServer_ReadyzPassesDeadline(t *testing.T) {
	t.Parallel()

	var hasDeadline bool

	s := httpserver.New(httpserver.Options{Ready: func(ctx context.Context) error {
		_, hasDeadline = ctx.Deadline()

		return nil
	}})
	do(t, s.Handler(), "GET", "/readyz")

	require.True(t, hasDeadline)
}

func listen(t *testing.T) net.Listener {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	return ln
}

func runAsync(ctx context.Context, s *httpserver.Server) <-chan error {
	done := make(chan error, 1)

	go func() { done <- s.Run(ctx) }()

	return done
}

func TestServer_RunServesAndStops(t *testing.T) {
	t.Parallel()

	ln := listen(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := runAsync(ctx, httpserver.New(httpserver.Options{Listener: ln, DrainTimeout: time.Second, Logger: zap.NewNop()}))

	resp, err := get(t, "http://"+ln.Addr().String()+"/healthz")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, "ok", string(body))

	cancel()
	require.NoError(t, <-done)
}

func TestServer_RunListensOnAddr(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.NoError(t, httpserver.New(httpserver.Options{Addr: "127.0.0.1:0", DrainTimeout: time.Second}).Run(ctx))
}

func TestServer_RunListenError(t *testing.T) {
	t.Parallel()

	err := httpserver.New(httpserver.Options{Addr: "127.0.0.1:-1"}).Run(t.Context())

	var opErr *net.OpError
	require.ErrorAs(t, err, &opErr)
}

func TestServer_RunServeError(t *testing.T) {
	t.Parallel()

	ln := listen(t)
	require.NoError(t, ln.Close())

	err := httpserver.New(httpserver.Options{Listener: ln}).Run(t.Context())

	require.ErrorIs(t, err, net.ErrClosed)
}

func TestServer_RunDrainsInFlightRequest(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte("finished"))
	})

	ln := listen(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := runAsync(ctx, httpserver.New(httpserver.Options{Listener: ln, API: api, DrainTimeout: 10 * time.Second}))

	type result struct {
		status int
		body   string
		err    error
	}

	resCh := make(chan result, 1)

	go func() {
		resp, err := get(t, "http://"+ln.Addr().String()+"/slow")
		if err != nil {
			resCh <- result{err: err}

			return
		}
		defer resp.Body.Close()

		b, err := io.ReadAll(resp.Body)
		resCh <- result{status: resp.StatusCode, body: string(b), err: err}
	}()

	<-started
	cancel()

	require.Eventually(t, func() bool {
		conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", ln.Addr().String())
		if err == nil {
			_ = conn.Close()
		}

		return err != nil
	}, 5*time.Second, 5*time.Millisecond)

	select {
	case err := <-done:
		t.Fatalf("Run returned before the in-flight request finished: %v", err)
	default:
	}

	close(release)

	require.Equal(t, result{status: 200, body: "finished"}, <-resCh)
	require.NoError(t, <-done)
}

func TestServer_RunDrainTimeout(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	api := http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		close(started)
		<-req.Context().Done()
	})

	ln := listen(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := runAsync(ctx, httpserver.New(httpserver.Options{Listener: ln, API: api, DrainTimeout: 50 * time.Millisecond}))

	go func() {
		resp, err := get(t, "http://"+ln.Addr().String()+"/stuck")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	<-started
	cancel()

	require.ErrorIs(t, <-done, context.DeadlineExceeded)
}
