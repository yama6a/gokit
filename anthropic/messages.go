package anthropic

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
)

// Request is one call with a single user message.
type Request struct {
	Model  string
	System string
	Prompt string
	// Images are sent before Prompt, in order.
	Images []Image
	// Schema, when set, makes the model answer with JSON that matches it.
	Schema any
	// MaxTokens caps the answer and the thinking together. The API requires it.
	MaxTokens int
	// Effort is low, medium, high, xhigh or max.
	Effort string
}

// Image is an encoded image with its MIME type.
type Image struct {
	MimeType string
	Data     []byte
}

// Usage counts the tokens of one call. OutputTokens includes thinking.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Response is the answer to a Request.
type Response struct {
	ID    string
	Model string
	// Text joins every text block of the answer.
	Text    string
	Usage   Usage
	CostUSD float64
	// Priced is false when the price table does not hold the model. CostUSD is then 0.
	Priced bool
}

type messageRequest struct {
	Model        string       `json:"model"`
	MaxTokens    int          `json:"max_tokens"`
	System       string       `json:"system,omitempty"`
	Messages     []message    `json:"messages"`
	OutputConfig outputConfig `json:"output_config,omitzero"`
}

type message struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type   string       `json:"type"`
	Text   string       `json:"text,omitempty"`
	Source *imageSource `json:"source,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type outputConfig struct {
	Effort string        `json:"effort,omitempty"`
	Format *outputFormat `json:"format,omitempty"`
}

type outputFormat struct {
	Type   string `json:"type"`
	Schema any    `json:"schema"`
}

type messageResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason  string `json:"stop_reason"`
	StopDetails *struct {
		Category    *string `json:"category"`
		Explanation string  `json:"explanation"`
	} `json:"stop_details"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Respond sends req. On ErrIncomplete and ErrRefused it still returns the Response, so the
// caller can record the usage and cost of the call.
func (c *Client) Respond(ctx context.Context, req Request) (Response, error) {
	content := make([]contentBlock, 0, len(req.Images)+1)
	for _, img := range req.Images {
		source := &imageSource{Type: "base64", MediaType: img.MimeType, Data: base64.StdEncoding.EncodeToString(img.Data)}
		content = append(content, contentBlock{Type: "image", Source: source})
	}
	content = append(content, contentBlock{Type: "text", Text: req.Prompt})

	payload := messageRequest{
		Model:        req.Model,
		MaxTokens:    req.MaxTokens,
		System:       req.System,
		Messages:     []message{{Role: "user", Content: content}},
		OutputConfig: outputConfig{Effort: req.Effort},
	}
	if req.Schema != nil {
		payload.OutputConfig.Format = &outputFormat{Type: "json_schema", Schema: req.Schema}
	}

	var m messageResponse
	if err := c.postJSON(ctx, "/v1/messages", payload, &m); err != nil {
		return Response{}, err
	}

	usage := Usage{InputTokens: m.Usage.InputTokens, OutputTokens: m.Usage.OutputTokens}
	resp := Response{ID: m.ID, Model: m.Model, Usage: usage}
	if p, ok := c.price(req.Model); ok {
		resp.CostUSD = usage.USD(p)
		resp.Priced = true
	}

	if err := m.stopError(); err != nil {
		return resp, err
	}

	var text strings.Builder
	for _, b := range m.Content {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	resp.Text = text.String()
	return resp, nil
}

func (m messageResponse) stopError() error {
	switch m.StopReason {
	case "end_turn":
		return nil
	case "refusal":
		var category, explanation string
		if m.StopDetails != nil {
			explanation = m.StopDetails.Explanation
			if m.StopDetails.Category != nil {
				category = *m.StopDetails.Category
			}
		}
		return fmt.Errorf("%w: category %q: %s", ErrRefused, category, explanation)
	default:
		return fmt.Errorf("%w: stop reason %q", ErrIncomplete, m.StopReason)
	}
}
