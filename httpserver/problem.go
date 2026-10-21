package httpserver

import (
	"encoding/json"
	"net/http"
)

// Problem is an RFC 9457 problem details document.
type Problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

// WriteProblem writes p as application/problem+json.
// An empty Type becomes "about:blank", an empty Title becomes the status text,
// and a zero Status becomes 500.
func WriteProblem(w http.ResponseWriter, p Problem) {
	if p.Status == 0 {
		p.Status = http.StatusInternalServerError
	}

	if p.Type == "" {
		p.Type = "about:blank"
	}

	if p.Title == "" {
		p.Title = http.StatusText(p.Status)
	}

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p) //nolint:errchkjson // Problem holds only strings and an int, so encoding cannot fail
}
