package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var (
	// ErrIncomplete is returned when an answer stops early, for example at max_tokens.
	ErrIncomplete = errors.New("anthropic: answer incomplete")
	// ErrRefused is returned when the model declines the request.
	ErrRefused = errors.New("anthropic: model refused the request")
)

// spendCapCode marks the 429 of a monthly spend cap, which lasts until the next month.
const spendCapCode = "enforced_spend_limit_reached"

var _ error = (*APIError)(nil)

// APIError is a non-200 answer of the API.
type APIError struct {
	Status  int
	Type    string
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("anthropic: status %d: %s (type %q, code %q)", e.Status, e.Message, e.Type, e.Code)
}

// Retryable reports whether the same request may succeed later: true for 429 and 5xx,
// false for a reached monthly spend cap, which Claude also reports as 429.
func (e *APIError) Retryable() bool {
	if e.Code == spendCapCode {
		return false
	}
	return e.Status == http.StatusTooManyRequests || e.Status >= http.StatusInternalServerError
}

func newAPIError(status int, body []byte) *APIError {
	var e struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Details struct {
				ErrorCode string `json:"error_code"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	if e.Error.Message == "" {
		e.Error.Message = strings.TrimSpace(string(body))
	}
	return &APIError{Status: status, Type: e.Error.Type, Code: e.Error.Details.ErrorCode, Message: e.Error.Message}
}
