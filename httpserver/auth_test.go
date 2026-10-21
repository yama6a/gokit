package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yama6a/gokit/httpserver"
)

func TestBearerAuth(t *testing.T) {
	t.Parallel()

	skipPublic := func(r *http.Request) bool { return r.URL.Path == "/public" }
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	unauthorized := `{"type":"about:blank","title":"Unauthorized","status":401,"detail":"missing or invalid bearer token"}` + "\n"

	tests := []struct {
		name       string
		token      string
		skip       func(*http.Request) bool
		path       string
		header     string
		wantStatus int
		wantBody   string
	}{
		{name: "valid token", token: "s3cret", path: "/x", header: "Bearer s3cret", wantStatus: 200, wantBody: "ok"},
		{name: "wrong token", token: "s3cret", path: "/x", header: "Bearer nope", wantStatus: 401, wantBody: unauthorized},
		{name: "longer token", token: "s3cret", path: "/x", header: "Bearer s3cret-and-more", wantStatus: 401, wantBody: unauthorized},
		{name: "missing header", token: "s3cret", path: "/x", wantStatus: 401, wantBody: unauthorized},
		{name: "wrong scheme", token: "s3cret", path: "/x", header: "Basic s3cret", wantStatus: 401, wantBody: unauthorized},
		{name: "empty token rejects empty header", token: "", path: "/x", wantStatus: 401, wantBody: unauthorized},
		{name: "empty token rejects bare Bearer", token: "", path: "/x", header: "Bearer ", wantStatus: 401, wantBody: unauthorized},
		{name: "skip passes without header", token: "s3cret", skip: skipPublic, path: "/public", wantStatus: 200, wantBody: "ok"},
		{name: "skip does not match other paths", token: "s3cret", skip: skipPublic, path: "/x", wantStatus: 401, wantBody: unauthorized},
		{name: "skip passes with empty token", token: "", skip: skipPublic, path: "/public", wantStatus: 200, wantBody: "ok"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := newRequest(t, http.MethodGet, tt.path)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}

			rec := httptest.NewRecorder()
			httpserver.BearerAuth(tt.token, tt.skip)(ok).ServeHTTP(rec, req)

			require.Equal(t, tt.wantStatus, rec.Code)
			require.Equal(t, tt.wantBody, rec.Body.String())

			if tt.wantStatus == http.StatusUnauthorized {
				require.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"))
				require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
			}
		})
	}
}
