package httpserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"
)

const (
	defaultReadHeaderTimeout = 10 * time.Second
	readyTimeout             = 5 * time.Second
)

// Options configures a Server.
type Options struct {
	Addr string
	// Listener replaces Addr when set. Tests use it to bind port 0.
	Listener net.Listener
	// Ready reports whether the process can take traffic. Nil means always ready.
	Ready func(ctx context.Context) error
	// Metrics serves GET /metrics. Nil answers 404.
	Metrics http.Handler
	// API serves every path the probes and metrics do not. Nil answers 404.
	API http.Handler
	// DrainTimeout is how long Run waits for in-flight requests after its context ends.
	DrainTimeout time.Duration
	// ReadHeaderTimeout bounds reading the request headers. Zero means 10 seconds.
	ReadHeaderTimeout time.Duration
	// ReadTimeout bounds reading the whole request. Zero means no limit.
	ReadTimeout time.Duration
	// WriteTimeout bounds the time from the end of the request headers to the end of the
	// response. Zero means no limit.
	WriteTimeout time.Duration
	// IdleTimeout bounds the wait for the next request on a keep-alive connection. Zero
	// means ReadTimeout, as in net/http.
	IdleTimeout time.Duration
	Logger      *zap.Logger
}

// Server serves probes, metrics and an API on one address.
type Server struct {
	opts Options
}

// New returns a Server. A nil Logger becomes a no-op logger.
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = zap.NewNop()
	}
	if opts.ReadHeaderTimeout == 0 {
		opts.ReadHeaderTimeout = defaultReadHeaderTimeout
	}

	return &Server{opts: opts}
}

// Handler returns the root handler that Run serves.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.Handle("GET /metrics", orNotFound(s.opts.Metrics))
	mux.Handle("/", orNotFound(s.opts.API))

	return mux
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if s.opts.Ready != nil {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()

		if err := s.opts.Ready(ctx); err != nil {
			s.opts.Logger.Warn("readiness check failed", zap.Error(err))
			http.Error(w, "not ready", http.StatusServiceUnavailable)

			return
		}
	}

	_, _ = w.Write([]byte("ok"))
}

func orNotFound(h http.Handler) http.Handler {
	if h == nil {
		return http.NotFoundHandler()
	}

	return h
}

func (s *Server) httpServer() *http.Server {
	return &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: s.opts.ReadHeaderTimeout,
		ReadTimeout:       s.opts.ReadTimeout,
		WriteTimeout:      s.opts.WriteTimeout,
		IdleTimeout:       s.opts.IdleTimeout,
	}
}

// Run serves until ctx ends, then drains in-flight requests for DrainTimeout.
// It returns nil after a clean drain.
func (s *Server) Run(ctx context.Context) error {
	ln := s.opts.Listener
	if ln == nil {
		var err error

		ln, err = (&net.ListenConfig{}).Listen(ctx, "tcp", s.opts.Addr)
		if err != nil {
			return fmt.Errorf("listen on %s: %w", s.opts.Addr, err)
		}
	}

	srv := s.httpServer()

	errCh := make(chan error, 1)

	go func() {
		defer close(errCh)

		errCh <- srv.Serve(ln)
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("serve on %s: %w", ln.Addr(), err)
	case <-ctx.Done():
	}

	s.opts.Logger.Info("shutting down", zap.Duration("drain_timeout", s.opts.DrainTimeout))

	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.opts.DrainTimeout)
	defer cancel()

	err := srv.Shutdown(drainCtx)
	if err != nil {
		_ = srv.Close()
	}

	for range errCh {
	}

	if err != nil {
		return fmt.Errorf("drain: %w", err)
	}

	return nil
}
