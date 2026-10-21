package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yama6a/gokit/httpserver"
)

func TestWriteProblem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   httpserver.Problem
		want httpserver.Problem
	}{
		{
			name: "defaults",
			in:   httpserver.Problem{Status: http.StatusNotFound},
			want: httpserver.Problem{Type: "about:blank", Title: "Not Found", Status: http.StatusNotFound},
		},
		{
			name: "all fields",
			in: httpserver.Problem{
				Type: "https://example.com/probs/out-of-credit", Title: "Out of credit", Status: http.StatusForbidden,
				Detail: "balance is 30", Instance: "/account/12345",
			},
			want: httpserver.Problem{
				Type: "https://example.com/probs/out-of-credit", Title: "Out of credit", Status: http.StatusForbidden,
				Detail: "balance is 30", Instance: "/account/12345",
			},
		},
		{
			name: "zero status",
			in:   httpserver.Problem{},
			want: httpserver.Problem{Type: "about:blank", Title: "Internal Server Error", Status: http.StatusInternalServerError},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			httpserver.WriteProblem(rec, tt.in)

			require.Equal(t, tt.want.Status, rec.Code)
			require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))

			var got httpserver.Problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			require.Equal(t, tt.want, got)
		})
	}
}

func TestWriteProblem_OmitsEmptyOptionalFields(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	httpserver.WriteProblem(rec, httpserver.Problem{Status: http.StatusBadRequest})

	require.JSONEq(t, `{"type":"about:blank","title":"Bad Request","status":400}`, rec.Body.String())
}
