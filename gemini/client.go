// Package gemini calls the Gemini Interactions API over plain net/http and prices every
// call from a caller-supplied price table.
package gemini

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

// DefaultBaseURL is the public Gemini API.
const DefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// maxResponseBytes bounds a response body. A base64 4K JPEG stays well below it.
const maxResponseBytes = 64 << 20

// Client calls the Gemini API with one API key.
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
		return fmt.Errorf("gemini %s: encode request: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("gemini %s: %w", path, err)
	}
	req.Header.Set("X-Goog-Api-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gemini %s: %w", path, err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("gemini %s: read body: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return newAPIError(resp.StatusCode, b)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("gemini %s: decode response: %w", path, err)
	}
	return nil
}

func (c *Client) price(model string) (Price, bool) {
	if p, ok := c.prices[model]; ok {
		return p, true
	}
	c.logger.Warn("gemini model has no price, cost recorded as 0", zap.String("model", model))
	return Price{}, false
}
