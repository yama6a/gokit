package httpserver

import (
	"crypto/subtle"
	"net/http"
)

// BearerAuth rejects requests whose Authorization header is not "Bearer <token>".
// Requests for which skip returns true pass through. An empty token rejects every other request.
func BearerAuth(token string, skip func(*http.Request) bool) func(http.Handler) http.Handler {
	want := []byte("Bearer " + token)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skip != nil && skip(r) {
				next.ServeHTTP(w, r)

				return
			}

			got := []byte(r.Header.Get("Authorization"))
			if token == "" || subtle.ConstantTimeCompare(got, want) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				WriteProblem(w, Problem{Status: http.StatusUnauthorized, Detail: "missing or invalid bearer token"})

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
