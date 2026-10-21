package openai

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
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
	Auth        string
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
		"gpt-5-mini":  {Input: 0.25, CachedInput: 0.125, Output: 2},
		"gpt-image-1": {Input: 5, CachedInput: 1.25, Output: 10, ImageInput: 10, ImageOutput: 40},
	}
}

// newTestClient serves one request with status and body and hands the request to the
// returned channel.
func newTestClient(t *testing.T, status int, body []byte) (*Client, <-chan recorded) {
	t.Helper()
	reqs := make(chan recorded, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reqs <- recorded{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), b}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL+"/v1/", "sk-test", srv.Client(), testPrices(), zap.NewNop()), reqs
}

func TestClient_Respond(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		req      Request
		wantBody string
		want     Response
	}{
		{
			name: "all fields",
			req: Request{
				Model:           "gpt-5-mini",
				Instructions:    "You write short social media posts.",
				Input:           "A post about autumn in Bergen.",
				ReasoningEffort: "low",
				MaxOutputTokens: 2000,
			},
			wantBody: `{
				"model": "gpt-5-mini",
				"instructions": "You write short social media posts.",
				"input": "A post about autumn in Bergen.",
				"reasoning": {"effort": "low"},
				"max_output_tokens": 2000,
				"store": false
			}`,
			want: Response{
				ID:      "resp_67ccd2bed1ec8190b14f964abc0542670bb6a6b452d3795b",
				Model:   "gpt-5-mini-2025-08-07",
				Text:    "Autumn light on the fjord. Coffee first, then the hike.",
				Usage:   Usage{InputTokens: 1800, CachedTokens: 1024, OutputTokens: 420},
				CostUSD: 0.001162,
				Priced:  true,
			},
		},
		{
			name:     "defaults omitted",
			req:      Request{Model: "gpt-5-mini", Input: "Hi"},
			wantBody: `{"model": "gpt-5-mini", "input": "Hi", "store": false}`,
			want: Response{
				ID:      "resp_67ccd2bed1ec8190b14f964abc0542670bb6a6b452d3795b",
				Model:   "gpt-5-mini-2025-08-07",
				Text:    "Autumn light on the fjord. Coffee first, then the hike.",
				Usage:   Usage{InputTokens: 1800, CachedTokens: 1024, OutputTokens: 420},
				CostUSD: 0.001162,
				Priced:  true,
			},
		},
		{
			name:     "unknown model is not priced",
			req:      Request{Model: "gpt-9", Input: "Hi"},
			wantBody: `{"model": "gpt-9", "input": "Hi", "store": false}`,
			want: Response{
				ID:    "resp_67ccd2bed1ec8190b14f964abc0542670bb6a6b452d3795b",
				Model: "gpt-5-mini-2025-08-07",
				Text:  "Autumn light on the fjord. Coffee first, then the hike.",
				Usage: Usage{InputTokens: 1800, CachedTokens: 1024, OutputTokens: 420},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, reqs := newTestClient(t, http.StatusOK, fixture(t, "responses_completed.json"))

			got, err := c.Respond(t.Context(), tt.req)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)

			r := <-reqs
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "/v1/responses", r.Path)
			require.Equal(t, "Bearer sk-test", r.Auth)
			require.Equal(t, "application/json", r.ContentType)
			require.JSONEq(t, tt.wantBody, string(r.Body))
		})
	}
}

func TestClient_RespondStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fixture string
		wantErr error
		want    Response
	}{
		{
			name:    "incomplete keeps partial text",
			fixture: "responses_incomplete.json",
			wantErr: ErrIncomplete,
			want: Response{
				ID:      "resp_68f7a1c0e5b48190a3a0d9a7e2f1b6c40aa1b2c3d4e5f6a7",
				Model:   "gpt-5-mini-2025-08-07",
				Text:    "Autumn light on",
				Usage:   Usage{InputTokens: 900, OutputTokens: 64},
				CostUSD: 0.000353,
				Priced:  true,
			},
		},
		{
			name:    "refusal",
			fixture: "responses_refusal.json",
			wantErr: ErrRefused,
			want: Response{
				ID:      "resp_68f7a2000a2c8190b6f1c3a4d5e6f7080aa1b2c3d4e5f6a7",
				Model:   "gpt-5-mini-2025-08-07",
				Usage:   Usage{InputTokens: 120, OutputTokens: 12},
				CostUSD: 0.000054,
				Priced:  true,
			},
		},
		{
			name:    "failed",
			fixture: "responses_failed.json",
			wantErr: ErrFailed,
			want: Response{
				ID:      "resp_68f7a3000b3d8190c7e2d4b5e6f708190aa1b2c3d4e5f6a7",
				Model:   "gpt-5-mini-2025-08-07",
				Usage:   Usage{InputTokens: 120},
				CostUSD: 0.00003,
				Priced:  true,
			},
		},
		{
			name:    "cancelled",
			fixture: "responses_cancelled.json",
			wantErr: ErrFailed,
			want: Response{
				ID:     "resp_68f7a4000c4e8190d8f3e5c6f708192a0aa1b2c3d4e5f6a7",
				Model:  "gpt-5-mini-2025-08-07",
				Priced: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, _ := newTestClient(t, http.StatusOK, fixture(t, tt.fixture))

			got, err := c.Respond(t.Context(), Request{Model: "gpt-5-mini", Input: "Hi"})
			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestClient_APIError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		status        int
		body          []byte
		want          *APIError
		wantRetryable bool
	}{
		{
			name:   "rate limit",
			status: http.StatusTooManyRequests,
			body:   fixture(t, "error_rate_limit.json"),
			want: &APIError{
				Status:  http.StatusTooManyRequests,
				Type:    "tokens",
				Code:    "rate_limit_exceeded",
				Message: "Rate limit reached for gpt-5-mini in organization org-abc123 on tokens per min (TPM): Limit 200000, Used 199100, Requested 2220. Please try again in 396ms.",
			},
			wantRetryable: true,
		},
		{
			name:   "insufficient quota",
			status: http.StatusTooManyRequests,
			body:   fixture(t, "error_insufficient_quota.json"),
			want: &APIError{
				Status:  http.StatusTooManyRequests,
				Type:    "insufficient_quota",
				Code:    "insufficient_quota",
				Message: "You exceeded your current quota, please check your plan and billing details.",
			},
		},
		{
			name:   "invalid api key",
			status: http.StatusUnauthorized,
			body:   fixture(t, "error_invalid_api_key.json"),
			want: &APIError{
				Status:  http.StatusUnauthorized,
				Type:    "invalid_request_error",
				Code:    "invalid_api_key",
				Message: "Incorrect API key provided: sk-test. You can find your API key at https://platform.openai.com/account/api-keys.",
			},
		},
		{
			name:   "invalid request",
			status: http.StatusBadRequest,
			body:   fixture(t, "error_invalid_request.json"),
			want: &APIError{
				Status:  http.StatusBadRequest,
				Type:    "invalid_request_error",
				Code:    "invalid_value",
				Message: "Invalid value: 'extreme'. Supported values are: 'minimal', 'low', 'medium', and 'high'.",
			},
		},
		{
			name:   "server error with null code",
			status: http.StatusInternalServerError,
			body:   fixture(t, "error_server.json"),
			want: &APIError{
				Status:  http.StatusInternalServerError,
				Type:    "server_error",
				Message: "The server had an error while processing your request. Sorry about that!",
			},
			wantRetryable: true,
		},
		{
			name:          "proxy error page",
			status:        http.StatusBadGateway,
			body:          []byte("<html><body>502 Bad Gateway</body></html>\n"),
			want:          &APIError{Status: http.StatusBadGateway, Message: "<html><body>502 Bad Gateway</body></html>"},
			wantRetryable: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, _ := newTestClient(t, tt.status, tt.body)

			_, err := c.Respond(t.Context(), Request{Model: "gpt-5-mini", Input: "Hi"})
			apiErr, ok := errors.AsType[*APIError](err)
			require.True(t, ok)
			require.Equal(t, tt.want, apiErr)
			require.Equal(t, tt.wantRetryable, apiErr.Retryable())
		})
	}
}

func TestAPIError_Error(t *testing.T) {
	t.Parallel()

	err := &APIError{Status: 429, Type: "tokens", Code: "rate_limit_exceeded", Message: "slow down"}
	require.EqualError(t, err, `openai: status 429: slow down (type "tokens", code "rate_limit_exceeded")`)
}

func TestClient_TransportErrors(t *testing.T) {
	t.Parallel()

	t.Run("invalid base url", func(t *testing.T) {
		t.Parallel()
		c := NewClient("://no-scheme", "sk-test", http.DefaultClient, nil, zap.NewNop())
		_, err := c.Respond(t.Context(), Request{Model: "gpt-5-mini"})
		require.Error(t, err)
	})

	t.Run("connection refused", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close()
		c := NewClient(srv.URL, "sk-test", http.DefaultClient, nil, zap.NewNop())
		_, err := c.Respond(t.Context(), Request{Model: "gpt-5-mini"})
		require.Error(t, err)
		_, isAPIErr := errors.AsType[*APIError](err)
		require.False(t, isAPIErr)
	})

	t.Run("cancelled context", func(t *testing.T) {
		t.Parallel()
		c, _ := newTestClient(t, http.StatusOK, fixture(t, "responses_completed.json"))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := c.Respond(ctx, Request{Model: "gpt-5-mini"})
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("truncated body", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "1000")
			_, _ = w.Write([]byte(`{"id":`))
		}))
		t.Cleanup(srv.Close)
		c := NewClient(srv.URL, "sk-test", srv.Client(), nil, zap.NewNop())
		_, err := c.Respond(t.Context(), Request{Model: "gpt-5-mini"})
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	})

	t.Run("invalid json", func(t *testing.T) {
		t.Parallel()
		c, _ := newTestClient(t, http.StatusOK, []byte("not json"))
		_, err := c.Respond(t.Context(), Request{Model: "gpt-5-mini"})
		require.Error(t, err)
		_, isAPIErr := errors.AsType[*APIError](err)
		require.False(t, isAPIErr)
	})
}

func TestClient_GenerateImages(t *testing.T) {
	t.Parallel()
	c, reqs := newTestClient(t, http.StatusOK, fixture(t, "images_generate.json"))

	got, err := c.GenerateImages(t.Context(), ImageRequest{
		Model:        "gpt-image-1",
		Prompt:       "A misty fjord at dawn, flat illustration",
		Size:         "1536x1024",
		Quality:      "medium",
		N:            2,
		Background:   "opaque",
		OutputFormat: "png",
	})
	require.NoError(t, err)
	require.Equal(t, ImageResult{
		Images:   [][]byte{[]byte("\x89PNG\r\n\x1a\nfirst"), []byte("\x89PNG\r\n\x1a\nsecond")},
		MimeType: "image/png",
		Usage: ImageUsage{
			InputTokens:       50,
			InputTextTokens:   50,
			OutputTokens:      2496,
			OutputImageTokens: 2496,
		},
		CostUSD: 0.10009,
		Priced:  true,
	}, got)

	r := <-reqs
	require.Equal(t, http.MethodPost, r.Method)
	require.Equal(t, "/v1/images/generations", r.Path)
	require.Equal(t, "Bearer sk-test", r.Auth)
	require.Equal(t, "application/json", r.ContentType)
	require.JSONEq(t, `{
		"model": "gpt-image-1",
		"prompt": "A misty fjord at dawn, flat illustration",
		"n": 2,
		"size": "1536x1024",
		"quality": "medium",
		"background": "opaque",
		"output_format": "png"
	}`, string(r.Body))
}

type formPart struct {
	FormName    string
	FileName    string
	ContentType string
	Data        string
}

func readMultipart(t *testing.T, contentType string, body []byte) []formPart {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)

	var parts []formPart
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return parts
		}
		require.NoError(t, err)
		data, err := io.ReadAll(p)
		require.NoError(t, err)
		parts = append(parts, formPart{p.FormName(), p.FileName(), p.Header.Get("Content-Type"), string(data)})
	}
}

func TestClient_EditImages(t *testing.T) {
	t.Parallel()
	c, reqs := newTestClient(t, http.StatusOK, fixture(t, "images_edit.json"))

	got, err := c.EditImages(t.Context(), ImageEditRequest{
		ImageRequest: ImageRequest{
			Model:        "gpt-image-1",
			Prompt:       "Put the mug on the boat deck",
			Size:         "1024x1024",
			Quality:      "high",
			N:            1,
			Background:   "opaque",
			OutputFormat: "jpeg",
		},
		Images: []NamedImage{
			{Name: "mug.png", MimeType: "image/png", Data: []byte("mug-bytes")},
			{Name: `boat "deck".webp`, MimeType: "image/webp", Data: []byte("boat-bytes")},
			{Name: "raw", Data: []byte("raw-bytes")},
		},
	})
	require.NoError(t, err)
	require.Equal(t, ImageResult{
		Images:   [][]byte{[]byte("\xff\xd8\xff\xe0edited")},
		MimeType: "image/jpeg",
		Usage: ImageUsage{
			InputTokens:       1157,
			InputTextTokens:   37,
			InputImageTokens:  1120,
			OutputTokens:      4180,
			OutputTextTokens:  20,
			OutputImageTokens: 4160,
		},
		CostUSD: 0.177985,
		Priced:  true,
	}, got)

	r := <-reqs
	require.Equal(t, http.MethodPost, r.Method)
	require.Equal(t, "/v1/images/edits", r.Path)
	require.Equal(t, "Bearer sk-test", r.Auth)
	require.Equal(t, []formPart{
		{FormName: "model", Data: "gpt-image-1"},
		{FormName: "prompt", Data: "Put the mug on the boat deck"},
		{FormName: "n", Data: "1"},
		{FormName: "size", Data: "1024x1024"},
		{FormName: "quality", Data: "high"},
		{FormName: "background", Data: "opaque"},
		{FormName: "output_format", Data: "jpeg"},
		{FormName: "image[]", FileName: "mug.png", ContentType: "image/png", Data: "mug-bytes"},
		{FormName: "image[]", FileName: `boat "deck".webp`, ContentType: "image/webp", Data: "boat-bytes"},
		{FormName: "image[]", FileName: "raw", ContentType: "application/octet-stream", Data: "raw-bytes"},
	}, readMultipart(t, r.ContentType, r.Body))
}

func TestClient_EditImagesOmitsEmptyFields(t *testing.T) {
	t.Parallel()
	c, reqs := newTestClient(t, http.StatusOK, fixture(t, "images_edit.json"))

	_, err := c.EditImages(t.Context(), ImageEditRequest{
		ImageRequest: ImageRequest{Model: "gpt-image-1", Prompt: "Brighter"},
		Images:       []NamedImage{{Name: "a.png", MimeType: "image/png", Data: []byte("a")}},
	})
	require.NoError(t, err)

	r := <-reqs
	require.Equal(t, []formPart{
		{FormName: "model", Data: "gpt-image-1"},
		{FormName: "prompt", Data: "Brighter"},
		{FormName: "image[]", FileName: "a.png", ContentType: "image/png", Data: "a"},
	}, readMultipart(t, r.ContentType, r.Body))
}

func TestClient_ImageErrors(t *testing.T) {
	t.Parallel()

	generateUsage := ImageUsage{InputTokens: 50, InputTextTokens: 50, OutputTokens: 2496, OutputImageTokens: 2496}
	tests := []struct {
		name       string
		status     int
		fixture    string
		call       func(context.Context, *Client) (ImageResult, error)
		wantErr    error
		wantAPIErr bool
		want       ImageResult
	}{
		{
			name:    "fewer images than requested",
			status:  http.StatusOK,
			fixture: "images_generate.json",
			call: func(ctx context.Context, c *Client) (ImageResult, error) {
				return c.GenerateImages(ctx, ImageRequest{Model: "gpt-image-1", Prompt: "p", N: 3})
			},
			wantErr: ErrImageCount,
			want: ImageResult{
				Images:   [][]byte{[]byte("\x89PNG\r\n\x1a\nfirst"), []byte("\x89PNG\r\n\x1a\nsecond")},
				MimeType: "image/png",
				Usage:    generateUsage,
				CostUSD:  0.10009,
				Priced:   true,
			},
		},
		{
			name:    "unknown model and default n",
			status:  http.StatusOK,
			fixture: "images_generate.json",
			call: func(ctx context.Context, c *Client) (ImageResult, error) {
				return c.GenerateImages(ctx, ImageRequest{Model: "gpt-image-9", Prompt: "p"})
			},
			wantErr: ErrImageCount,
			want: ImageResult{
				Images:   [][]byte{[]byte("\x89PNG\r\n\x1a\nfirst"), []byte("\x89PNG\r\n\x1a\nsecond")},
				MimeType: "image/png",
				Usage:    generateUsage,
			},
		},
		{
			name:    "generate api error",
			status:  http.StatusTooManyRequests,
			fixture: "error_rate_limit.json",
			call: func(ctx context.Context, c *Client) (ImageResult, error) {
				return c.GenerateImages(ctx, ImageRequest{Model: "gpt-image-1", Prompt: "p"})
			},
			wantAPIErr: true,
		},
		{
			name:    "edit api error",
			status:  http.StatusInternalServerError,
			fixture: "error_server.json",
			call: func(ctx context.Context, c *Client) (ImageResult, error) {
				return c.EditImages(ctx, ImageEditRequest{ImageRequest: ImageRequest{Model: "gpt-image-1", Prompt: "p"}})
			},
			wantAPIErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, _ := newTestClient(t, tt.status, fixture(t, tt.fixture))

			got, err := tt.call(t.Context(), c)
			if tt.wantAPIErr {
				_, ok := errors.AsType[*APIError](err)
				require.True(t, ok)
			} else {
				require.ErrorIs(t, err, tt.wantErr)
			}
			require.Equal(t, tt.want, got)
		})
	}
}

func TestClient_ImageBadBase64(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, http.StatusOK, fixture(t, "images_bad_base64.json"))

	got, err := c.GenerateImages(t.Context(), ImageRequest{Model: "gpt-image-1", Prompt: "p"})
	require.Error(t, err)
	require.Equal(t, ImageResult{
		Images:   [][]byte{},
		MimeType: "image/png",
		Usage:    ImageUsage{InputTokens: 50, InputTextTokens: 50, OutputTokens: 1056, OutputImageTokens: 1056},
		CostUSD:  0.04249,
		Priced:   true,
	}, got)
}
