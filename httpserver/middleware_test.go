package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/yama6a/gokit/httpserver"
)

func newRouter(logger *zap.Logger) *chi.Mux {
	r := chi.NewRouter()
	r.Use(httpserver.Middleware(logger)...)
	r.Get("/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-Id", middleware.GetReqID(r.Context()))
		_, _ = w.Write([]byte("hello"))
	})
	r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })

	return r
}

func TestMiddleware_LogsRequest(t *testing.T) {
	t.Parallel()

	core, logs := observer.New(zap.InfoLevel)
	rec := httptest.NewRecorder()
	req := newRequest(t, http.MethodGet, "/hello")
	req.Header.Set("X-Request-ID", "req-1")

	newRouter(zap.New(core)).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "req-1", rec.Header().Get("X-Seen-Id"))

	entries := logs.All()
	require.Len(t, entries, 1)
	require.Equal(t, "request", entries[0].Message)

	fields := entries[0].ContextMap()
	require.Equal(t, "GET", fields["method"])
	require.Equal(t, "/hello", fields["path"])
	require.EqualValues(t, 200, fields["status"])
	require.EqualValues(t, 5, fields["bytes"])
	require.Equal(t, "req-1", fields["request_id"])
	require.Contains(t, fields, "duration")
}

func TestMiddleware_GeneratesRequestID(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	newRouter(zap.NewNop()).ServeHTTP(rec, newRequest(t, http.MethodGet, "/hello"))

	require.NotEmpty(t, rec.Header().Get("X-Seen-Id"))
}

func TestMiddleware_RecoversPanicAndLogs500(t *testing.T) {
	t.Parallel()

	core, logs := observer.New(zap.InfoLevel)
	rec := httptest.NewRecorder()

	newRouter(zap.New(core)).ServeHTTP(rec, newRequest(t, http.MethodGet, "/boom"))

	require.Equal(t, http.StatusInternalServerError, rec.Code)

	entries := logs.All()
	require.Len(t, entries, 1)
	require.EqualValues(t, 500, entries[0].ContextMap()["status"])
}
