package anthropic

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type recorded struct {
	Method      string
	Path        string
	APIKey      string
	Version     string
	ContentType string
	Body        []byte
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return b
}

func testPrices() PriceTable {
	return PriceTable{"claude-sonnet-5-5": {Input: 2, Output: 10}}
}

// newTestClient serves one request with status and body and hands the request to the
// returned channel.
func newTestClient(t *testing.T, status int, body []byte, logger *zap.Logger) (*Client, <-chan recorded) {
	t.Helper()
	reqs := make(chan recorded, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reqs <- recorded{
			r.Method, r.URL.Path, r.Header.Get("X-Api-Key"), r.Header.Get("Anthropic-Version"),
			r.Header.Get("Content-Type"), b,
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL+"/", "sk-test", srv.Client(), testPrices(), logger), reqs
}

func TestClient_Respond(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		req      Request
		wantBody string
	}{
		{
			name: "plain prompt",
			req:  Request{Model: "claude-sonnet-5-5", Prompt: "A post about the Riksbank.", MaxTokens: 16000},
			wantBody: `{
				"model": "claude-sonnet-5-5",
				"max_tokens": 16000,
				"messages": [{"role": "user", "content": [{"type": "text", "text": "A post about the Riksbank."}]}]
			}`,
		},
		{
			name: "system prompt",
			req: Request{
				Model: "claude-sonnet-5-5", System: "You write short social media posts.",
				Prompt: "A post about the Riksbank.", MaxTokens: 16000,
			},
			wantBody: `{
				"model": "claude-sonnet-5-5",
				"max_tokens": 16000,
				"system": "You write short social media posts.",
				"messages": [{"role": "user", "content": [{"type": "text", "text": "A post about the Riksbank."}]}]
			}`,
		},
		{
			name: "schema and effort",
			req: Request{
				Model: "claude-sonnet-5-5", Prompt: "A post about the Riksbank.", MaxTokens: 16000, Effort: "high",
				Schema: map[string]any{"type": "object", "required": []string{"text"}},
			},
			wantBody: `{
				"model": "claude-sonnet-5-5",
				"max_tokens": 16000,
				"messages": [{"role": "user", "content": [{"type": "text", "text": "A post about the Riksbank."}]}],
				"output_config": {
					"effort": "high",
					"format": {"type": "json_schema", "schema": {"type": "object", "required": ["text"]}}
				}
			}`,
		},
		{
			name: "two images",
			req: Request{
				Model: "claude-sonnet-5-5", Prompt: "Describe the photos.", MaxTokens: 16000,
				Images: []Image{{MimeType: "image/jpeg", Data: []byte("one")}, {MimeType: "image/png", Data: []byte("two")}},
			},
			wantBody: `{
				"model": "claude-sonnet-5-5",
				"max_tokens": 16000,
				"messages": [{"role": "user", "content": [
					{"type": "image", "source": {"type": "base64", "media_type": "image/jpeg", "data": "b25l"}},
					{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "dHdv"}},
					{"type": "text", "text": "Describe the photos."}
				]}]
			}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, reqs := newTestClient(t, http.StatusOK, fixture(t, "message_completed.json"), zap.NewNop())

			got, err := c.Respond(context.Background(), tt.req)
			require.NoError(t, err)

			req := <-reqs
			require.Equal(t, http.MethodPost, req.Method)
			require.Equal(t, "/v1/messages", req.Path)
			require.Equal(t, "sk-test", req.APIKey)
			require.Equal(t, "2026-10-21", req.Version)
			require.Equal(t, "application/json", req.ContentType)
			require.JSONEq(t, tt.wantBody, string(req.Body))

			require.Equal(t, Response{
				ID:      "msg_1",
				Model:   "claude-sonnet-5-5",
				Text:    `{"text":"Riksbanken sänker räntan"}`,
				Usage:   Usage{InputTokens: 1500, OutputTokens: 200},
				CostUSD: 0.005,
				Priced:  true,
			}, got)
		})
	}
}

func TestClient_Respond_StopReasons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fixture string
		wantErr error
		wantMsg string
	}{
		{
			fixture: "message_refusal.json",
			wantErr: ErrRefused,
			wantMsg: `anthropic: model refused the request: category "general_harms": The request asks for harmful content.`,
		},
		{
			fixture: "message_refusal_no_details.json",
			wantErr: ErrRefused,
			wantMsg: `anthropic: model refused the request: category "": `,
		},
		{
			fixture: "message_max_tokens.json",
			wantErr: ErrIncomplete,
			wantMsg: `anthropic: answer incomplete: stop reason "max_tokens"`,
		},
		{
			fixture: "message_context_exceeded.json",
			wantErr: ErrIncomplete,
			wantMsg: `anthropic: answer incomplete: stop reason "model_context_window_exceeded"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			t.Parallel()
			c, _ := newTestClient(t, http.StatusOK, fixture(t, tt.fixture), zap.NewNop())

			got, err := c.Respond(context.Background(), Request{Model: "claude-sonnet-5-5", Prompt: "x", MaxTokens: 100})

			require.ErrorIs(t, err, tt.wantErr)
			require.EqualError(t, err, tt.wantMsg)
			require.Empty(t, got.Text)
			require.Equal(t, Usage{InputTokens: 100, OutputTokens: 50}, got.Usage)
			require.InDelta(t, (100*2+50*10)/1e6, got.CostUSD, 1e-12)
			require.True(t, got.Priced)
		})
	}
}

func TestClient_Respond_APIErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fixture   string
		status    int
		want      APIError
		retryable bool
	}{
		{
			fixture: "error_invalid_request.json",
			status:  http.StatusBadRequest,
			want:    APIError{Status: 400, Type: "invalid_request_error", Message: "max_tokens: Field required"},
		},
		{
			fixture: "error_billing.json",
			status:  http.StatusPaymentRequired,
			want:    APIError{Status: 402, Type: "billing_error", Message: "Your credit balance is too low."},
		},
		{
			fixture: "error_rate_limit.json",
			status:  http.StatusTooManyRequests,
			want: APIError{
				Status: 429, Type: "rate_limit_error", Message: "Number of requests has exceeded your rate limit.",
			},
			retryable: true,
		},
		{
			fixture: "error_spend_cap.json",
			status:  http.StatusTooManyRequests,
			want: APIError{
				Status: 429, Type: "rate_limit_error", Code: "enforced_spend_limit_reached",
				Message: "You have reached your monthly spend limit.",
			},
		},
		{
			fixture:   "error_overloaded.json",
			status:    529,
			want:      APIError{Status: 529, Type: "overloaded_error", Message: "Overloaded"},
			retryable: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			t.Parallel()
			c, _ := newTestClient(t, tt.status, fixture(t, tt.fixture), zap.NewNop())

			_, err := c.Respond(context.Background(), Request{Model: "claude-sonnet-5-5", Prompt: "x", MaxTokens: 100})

			var apiErr *APIError
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, tt.want, *apiErr)
			require.Equal(t, tt.retryable, apiErr.Retryable())
		})
	}
}

func TestClient_Respond_APIErrorWithoutJSON(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, http.StatusBadGateway, []byte("upstream connect error\n"), zap.NewNop())

	_, err := c.Respond(context.Background(), Request{Model: "claude-sonnet-5-5", Prompt: "x", MaxTokens: 100})

	require.EqualError(t, err, `anthropic: status 502: upstream connect error (type "", code "")`)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	require.True(t, apiErr.Retryable())
}

func TestClient_Respond_UnknownModel(t *testing.T) {
	t.Parallel()
	core, logs := observer.New(zap.WarnLevel)
	c, _ := newTestClient(t, http.StatusOK, fixture(t, "message_completed.json"), zap.New(core))

	got, err := c.Respond(context.Background(), Request{Model: "claude-unknown", Prompt: "x", MaxTokens: 100})

	require.NoError(t, err)
	require.False(t, got.Priced)
	require.Zero(t, got.CostUSD)
	require.Equal(t, 1, logs.FilterMessage("anthropic model has no price, cost recorded as 0").Len())
}
