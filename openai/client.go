// Package openai calls the OpenAI Responses and Images APIs over plain net/http and
// prices every call from a caller-supplied price table.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"go.uber.org/zap"
)

// DefaultBaseURL is the public OpenAI API.
const DefaultBaseURL = "https://api.openai.com/v1"

// maxResponseBytes bounds a response body. Four base64 images of 1536x1024 px stay well
// below it.
const maxResponseBytes = 64 << 20

// Client calls the OpenAI API with one API key.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	prices  PriceTable
	logger  *zap.Logger
}

// NewClient returns a client that sends every request through hc and never retries, so
// any retry policy belongs on hc.
func NewClient(baseURL, apiKey string, hc *http.Client, prices PriceTable, logger *zap.Logger) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		apiKey:  apiKey,
		http:    hc,
		prices:  prices,
		logger:  logger,
	}
}

func (c *Client) postJSON(ctx context.Context, path string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("openai %s: encode request: %w", path, err)
	}
	return c.post(ctx, path, "application/json", body, out)
}

func (c *Client) post(ctx context.Context, path, contentType string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("openai %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", contentType)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("openai %s: %w", path, err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("openai %s: read body: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return newAPIError(resp.StatusCode, b)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("openai %s: decode response: %w", path, err)
	}
	return nil
}

// price returns the price of the first model in the table. A response names a dated
// snapshot such as gpt-5-mini-2025-08-07, so callers pass the requested model first.
func (c *Client) price(models ...string) (Price, bool) {
	for _, m := range models {
		if p, ok := c.prices[m]; ok {
			return p, true
		}
	}
	c.logger.Warn("openai model has no price, cost recorded as 0", zap.Strings("models", models))
	return Price{}, false
}
