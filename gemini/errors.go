package gemini

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var (
	// ErrIncomplete is returned when an interaction stops early, for example at the output token limit.
	ErrIncomplete = errors.New("gemini: interaction incomplete")
	// ErrFailed is returned when an interaction ends in a status other than completed or incomplete.
	ErrFailed = errors.New("gemini: interaction failed")
	// ErrNoImage is returned when an image interaction completes without an image.
	ErrNoImage = errors.New("gemini: no image in the answer")
)

var _ error = (*APIError)(nil)

// APIError is a non-200 answer of the API.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("gemini: status %d: %s (code %q)", e.Status, e.Message, e.Code)
}

// Retryable reports whether the same request may succeed later: true for 429 and 5xx,
// false for an exhausted daily quota, which Gemini also reports as 429.
func (e *APIError) Retryable() bool {
	if e.Code == "quota_exceeded" {
		return false
	}
	return e.Status == http.StatusTooManyRequests || e.Status >= http.StatusInternalServerError
}

func newAPIError(status int, body []byte) *APIError {
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	if e.Error.Message == "" {
		e.Error.Message = strings.TrimSpace(string(body))
	}
	return &APIError{Status: status, Code: e.Error.Code, Message: e.Error.Message}
}
