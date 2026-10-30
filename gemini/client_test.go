package gemini

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
)

type recorded struct {
	Method      string
	Path        string
	APIKey      string
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
	return PriceTable{
		"gemini-4-argon":         {Input: 2, CachedInput: 0.1, Output: 10},
		"gemini-nano-banana-2.1": {Input: 1.5, Output: 7.5, ImageOutput: 30},
	}
}

// newTestClient serves one request with status and body and hands the request to the
// returned channel.
func newTestClient(t *testing.T, status int, body []byte) (*Client, <-chan recorded) {
	t.Helper()
	reqs := make(chan recorded, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reqs <- recorded{r.Method, r.URL.Path, r.Header.Get("X-Goog-Api-Key"), r.Header.Get("Content-Type"), b}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL+"/v1beta/", "gm-test", srv.Client(), testPrices(), zap.NewNop()), reqs
}

func TestClient_Respond(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		req      TextRequest
		wantBody string
	}{
		{
			name: "schema and system instruction",
			req: TextRequest{
				Model:             "gemini-4-argon",
				SystemInstruction: "You write short social media posts.",
				Input:             "A post about the Riksbank.",
				Schema:            map[string]any{"type": "object", "required": []string{"text"}},
			},
			wantBody: `{
				"model": "gemini-4-argon",
				"input": "A post about the Riksbank.",
				"system_instruction": "You write short social media posts.",
				"response_format": {
					"type": "text",
					"mime_type": "application/json",
					"schema": {"type": "object", "required": ["text"]}
				},
				"store": false
			}`,
		},
		{
			name:     "plain text",
			req:      TextRequest{Model: "gemini-4-argon", Input: "A post about the Riksbank."},
			wantBody: `{"model": "gemini-4-argon", "input": "A post about the Riksbank.", "store": false}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, reqs := newTestClient(t, http.StatusOK, fixture(t, "text_completed.json"))

			got, err := c.Respond(context.Background(), tt.req)
			require.NoError(t, err)

			req := <-reqs
			require.Equal(t, http.MethodPost, req.Method)
			require.Equal(t, "/v1beta/interactions", req.Path)
			require.Equal(t, "gm-test", req.APIKey)
			require.Equal(t, "application/json", req.ContentType)
			require.JSONEq(t, tt.wantBody, string(req.Body))

			usage := Usage{InputTokens: 1500, CachedTokens: 1000, OutputTokens: 200, ThoughtTokens: 300}
			require.Equal(t, Response{
				ID:      "v1_ChdPU0F4YWFtNkFwS2kxZThQZ05lbXdROBIX",
				Model:   "gemini-4-argon",
				Text:    `{"text":"Riksbanken sänker räntan","topicTag":"ränta"}`,
				Usage:   usage,
				CostUSD: (500*2 + 1000*0.1 + 500*10) / 1e6,
				Priced:  true,
			}, got)
		})
	}
}

func TestClient_RespondIncomplete(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, http.StatusOK, fixture(t, "text_incomplete.json"))

	got, err := c.Respond(context.Background(), TextRequest{Model: "gemini-4-argon", Input: "x"})

	require.ErrorIs(t, err, ErrIncomplete)
	require.EqualError(t, err, "gemini: interaction incomplete: max_tokens: The output reached the token limit.")
	require.Equal(t, Response{
		ID:      "v1_incomplete",
		Model:   "gemini-4-argon",
		Text:    "Riksbanken sänker",
		Usage:   Usage{InputTokens: 100, OutputTokens: 50},
		CostUSD: (100*2 + 50*10) / 1e6,
		Priced:  true,
	}, got)
}

func TestClient_RespondUnpricedModel(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, http.StatusOK, fixture(t, "text_incomplete.json"))

	got, err := c.Respond(context.Background(), TextRequest{Model: "gemini-unknown", Input: "x"})

	require.ErrorIs(t, err, ErrIncomplete)
	require.False(t, got.Priced)
	require.Zero(t, got.CostUSD)
}

func TestClient_GenerateImage(t *testing.T) {
	t.Parallel()
	c, reqs := newTestClient(t, http.StatusOK, fixture(t, "image_completed.json"))

	got, err := c.GenerateImage(context.Background(), ImageRequest{
		Model:       "gemini-nano-banana-2.1",
		Prompt:      "A chart of the policy rate.",
		References:  []InlineImage{{MimeType: "image/png", Data: []byte("logo")}},
		AspectRatio: "16:9",
		ImageSize:   "2K",
	})
	require.NoError(t, err)

	req := <-reqs
	require.JSONEq(t, `{
		"model": "gemini-nano-banana-2.1",
		"input": [
			{"type": "text", "text": "A chart of the policy rate."},
			{"type": "image", "mime_type": "image/png", "data": "bG9nbw=="}
		],
		"response_format": {"type": "image", "mime_type": "image/jpeg", "aspect_ratio": "16:9", "image_size": "2K"},
		"store": false
	}`, string(req.Body))

	usage := Usage{
		InputTokens: 2340, InputImageTokens: 2240, OutputTokens: 1700, OutputImageTokens: 1680, ThoughtTokens: 400,
	}
	require.Equal(t, ImageResult{
		ID:       "v1_image",
		Model:    "gemini-nano-banana-2.1",
		Image:    []byte("\xff\xd8\xfffinal"),
		MimeType: "image/jpeg",
		Usage:    usage,
		CostUSD:  (2340*1.5 + 420*7.5 + 1680*30) / 1e6,
		Priced:   true,
	}, got)
}

func TestClient_GenerateImageWithoutOptions(t *testing.T) {
	t.Parallel()
	c, reqs := newTestClient(t, http.StatusOK, fixture(t, "image_completed.json"))

	_, err := c.GenerateImage(context.Background(), ImageRequest{Model: "gemini-nano-banana-2.1", Prompt: "A cat."})
	require.NoError(t, err)

	require.JSONEq(t, `{
		"model": "gemini-nano-banana-2.1",
		"input": [{"type": "text", "text": "A cat."}],
		"response_format": {"type": "image", "mime_type": "image/jpeg"},
		"store": false
	}`, string((<-reqs).Body))
}

func TestClient_GenerateImageFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fixture string
		wantErr error
		wantMsg string
	}{
		{
			fixture: "image_blocked.json",
			wantErr: ErrFailed,
			wantMsg: `gemini: interaction failed: status "failed": image_safety: Safety violations blocked image generation.`,
		},
		{fixture: "image_none.json", wantErr: ErrNoImage, wantMsg: "gemini: no image in the answer"},
		{fixture: "image_bad_base64.json", wantMsg: "gemini: decode image: illegal base64 data at input byte 3"},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			t.Parallel()
			c, _ := newTestClient(t, http.StatusOK, fixture(t, tt.fixture))

			got, err := c.GenerateImage(context.Background(), ImageRequest{Model: "gemini-nano-banana-2.1", Prompt: "x"})

			require.EqualError(t, err, tt.wantMsg)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}
			require.Nil(t, got.Image)
			require.True(t, got.Priced)
		})
	}
}

func TestClient_APIErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fixture   string
		status    int
		want      APIError
		retryable bool
	}{
		{
			fixture:   "error_rate_limit_exceeded.json",
			status:    http.StatusTooManyRequests,
			want:      APIError{Status: 429, Code: "rate_limit_exceeded", Message: "You have exceeded the per-minute request limit."},
			retryable: true,
		},
		{
			fixture: "error_quota_exceeded.json",
			status:  http.StatusTooManyRequests,
			want:    APIError{Status: 429, Code: "quota_exceeded", Message: "You have exceeded your daily quota."},
		},
		{
			fixture: "error_payment_required.json",
			status:  http.StatusPaymentRequired,
			want:    APIError{Status: 402, Code: "payment_required", Message: "Your Prepay credit balance is depleted."},
		},
		{
			fixture:   "error_service_unavailable.json",
			status:    http.StatusServiceUnavailable,
			want:      APIError{Status: 503, Code: "service_unavailable", Message: "The service is temporarily overloaded."},
			retryable: true,
		},
		{
			fixture: "error_invalid_request.json",
			status:  http.StatusBadRequest,
			want: APIError{
				Status: 400, Code: "invalid_request", Message: "The value 'x' is not supported for 'type' at 'input[0]'.",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			t.Parallel()
			c, _ := newTestClient(t, tt.status, fixture(t, tt.fixture))

			_, err := c.Respond(context.Background(), TextRequest{Model: "gemini-4-argon", Input: "x"})

			var apiErr *APIError
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, tt.want, *apiErr)
			require.Equal(t, tt.retryable, apiErr.Retryable())
		})
	}
}

func TestClient_APIErrorWithoutJSON(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, http.StatusBadGateway, []byte("upstream connect error\n"))

	_, err := c.GenerateImage(context.Background(), ImageRequest{Model: "gemini-nano-banana-2.1", Prompt: "x"})

	require.EqualError(t, err, `gemini: status 502: upstream connect error (code "")`)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	require.True(t, apiErr.Retryable())
}
