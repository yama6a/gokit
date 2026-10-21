package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var (
	// ErrRefused is returned when the model refuses to answer.
	ErrRefused = errors.New("openai: model refused")
	// ErrIncomplete is returned when a response stops early, for example at MaxOutputTokens.
	ErrIncomplete = errors.New("openai: response incomplete")
	// ErrFailed is returned when a response ends in a status other than completed or incomplete.
	ErrFailed = errors.New("openai: response failed")
	// ErrInvalidRequest is returned before any call when a Request is inconsistent.
	ErrInvalidRequest = errors.New("openai: invalid request")
	// ErrImageCount is returned when the API returns another number of images than requested.
	ErrImageCount = errors.New("openai: unexpected image count")
)

var _ error = (*APIError)(nil)

// APIError is a non-200 answer of the API.
type APIError struct {
	Status  int
	Type    string
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("openai: status %d: %s (type %q, code %q)", e.Status, e.Message, e.Type, e.Code)
}

// Retryable reports whether the same request may succeed later: true for 429 and 5xx,
// false for an exhausted quota, which OpenAI also reports as 429.
func (e *APIError) Retryable() bool {
	if e.Code == "insufficient_quota" || e.Type == "insufficient_quota" {
		return false
	}
	return e.Status == http.StatusTooManyRequests || e.Status >= http.StatusInternalServerError
}

func newAPIError(status int, body []byte) *APIError {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	if e.Error.Message == "" {
		e.Error.Message = strings.TrimSpace(string(body))
	}
	return &APIError{Status: status, Type: e.Error.Type, Code: e.Error.Code, Message: e.Error.Message}
}
