package gemini

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
)

// imageMimeType is the one image output type the Interactions API offers.
const imageMimeType = "image/jpeg"

// TextRequest is one text interaction.
type TextRequest struct {
	Model             string
	SystemInstruction string
	Input             string
	// Schema, when set, makes the model answer with JSON that matches it.
	Schema any
}

// ImageRequest is one image interaction, which returns one JPEG.
type ImageRequest struct {
	Model  string
	Prompt string
	// References are sent inline after the prompt.
	References []InlineImage
	// AspectRatio is for example "16:9". Empty lets the model choose.
	AspectRatio string
	// ImageSize is "1K", "2K" or "4K". Empty leaves the API default of 1K.
	ImageSize string
}

// InlineImage is an encoded image with its MIME type.
type InlineImage struct {
	MimeType string
	Data     []byte
}

// Usage counts the tokens of one interaction. InputTokens includes CachedTokens and
// InputImageTokens. OutputTokens includes OutputImageTokens and excludes ThoughtTokens.
type Usage struct {
	InputTokens       int
	CachedTokens      int
	InputImageTokens  int
	OutputTokens      int
	OutputImageTokens int
	ThoughtTokens     int
}

// Response is the answer to a TextRequest.
type Response struct {
	ID    string
	Model string
	// Text joins every text part of the model_output steps.
	Text    string
	Usage   Usage
	CostUSD float64
	// Priced is false when the price table does not hold the model. CostUSD is then 0.
	Priced bool
}

// ImageResult is the answer to an ImageRequest.
type ImageResult struct {
	ID       string
	Model    string
	Image    []byte
	MimeType string
	Usage    Usage
	CostUSD  float64
	// Priced is false when the price table does not hold the model. CostUSD is then 0.
	Priced bool
}

type content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type responseFormat struct {
	Type        string `json:"type"`
	MimeType    string `json:"mime_type,omitempty"`
	Schema      any    `json:"schema,omitempty"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
	ImageSize   string `json:"image_size,omitempty"`
}

type interactionRequest struct {
	Model             string          `json:"model"`
	Input             any             `json:"input"`
	SystemInstruction string          `json:"system_instruction,omitempty"`
	ResponseFormat    *responseFormat `json:"response_format,omitempty"`
	Store             bool            `json:"store"`
}

type modalityTokens struct {
	Modality string `json:"modality"`
	Tokens   int    `json:"tokens"`
}

type interaction struct {
	ID     string `json:"id"`
	Model  string `json:"model"`
	Status string `json:"status"`
	Steps  []struct {
		Type    string    `json:"type"`
		Content []content `json:"content"`
	} `json:"steps"`
	Errors []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Usage struct {
		TotalInputTokens       int              `json:"total_input_tokens"`
		InputTokensByModality  []modalityTokens `json:"input_tokens_by_modality"`
		TotalCachedTokens      int              `json:"total_cached_tokens"`
		TotalOutputTokens      int              `json:"total_output_tokens"`
		OutputTokensByModality []modalityTokens `json:"output_tokens_by_modality"`
		TotalThoughtTokens     int              `json:"total_thought_tokens"`
	} `json:"usage"`
}

// Respond sends req with store set to false. On ErrIncomplete and ErrFailed it still
// returns the Response, so the caller can record the usage and cost of the call.
func (c *Client) Respond(ctx context.Context, req TextRequest) (Response, error) {
	payload := interactionRequest{Model: req.Model, Input: req.Input, SystemInstruction: req.SystemInstruction}
	if req.Schema != nil {
		payload.ResponseFormat = &responseFormat{Type: "text", MimeType: "application/json", Schema: req.Schema}
	}
	var r interaction
	if err := c.postJSON(ctx, "/interactions", payload, &r); err != nil {
		return Response{}, err
	}

	resp := Response{ID: r.ID, Model: r.Model, Usage: r.usage()}
	if p, ok := c.price(req.Model); ok {
		resp.CostUSD = resp.Usage.USD(p)
		resp.Priced = true
	}
	var b strings.Builder
	for _, part := range r.output() {
		if part.Type == "text" {
			b.WriteString(part.Text)
		}
	}
	resp.Text = b.String()
	return resp, r.err()
}

// GenerateImage sends req with store set to false and returns the last image of the
// answer. On an error after the call it still returns the ImageResult with its usage and
// cost.
func (c *Client) GenerateImage(ctx context.Context, req ImageRequest) (ImageResult, error) {
	input := make([]content, 0, len(req.References)+1)
	input = append(input, content{Type: "text", Text: req.Prompt})
	for _, ref := range req.References {
		input = append(input, content{
			Type:     "image",
			MimeType: ref.MimeType,
			Data:     base64.StdEncoding.EncodeToString(ref.Data),
		})
	}
	payload := interactionRequest{
		Model: req.Model,
		Input: input,
		ResponseFormat: &responseFormat{
			Type:        "image",
			MimeType:    imageMimeType,
			AspectRatio: req.AspectRatio,
			ImageSize:   req.ImageSize,
		},
	}
	var r interaction
	if err := c.postJSON(ctx, "/interactions", payload, &r); err != nil {
		return ImageResult{}, err
	}

	res := ImageResult{ID: r.ID, Model: r.Model, Usage: r.usage()}
	if p, ok := c.price(req.Model); ok {
		res.CostUSD = res.Usage.USD(p)
		res.Priced = true
	}
	if err := r.err(); err != nil {
		return res, err
	}
	var last *content
	for _, part := range r.output() {
		if part.Type == "image" {
			last = &part
		}
	}
	if last == nil {
		return res, ErrNoImage
	}
	img, err := base64.StdEncoding.DecodeString(last.Data)
	if err != nil {
		return res, fmt.Errorf("gemini: decode image: %w", err)
	}
	res.Image, res.MimeType = img, last.MimeType
	if res.MimeType == "" {
		res.MimeType = imageMimeType
	}
	return res, nil
}

// output skips the thought steps, which hold the model's draft images.
func (r *interaction) output() []content {
	var out []content
	for _, s := range r.Steps {
		if s.Type == "model_output" {
			out = append(out, s.Content...)
		}
	}
	return out
}

func (r *interaction) usage() Usage {
	u := r.Usage
	return Usage{
		InputTokens:       u.TotalInputTokens,
		CachedTokens:      u.TotalCachedTokens,
		InputImageTokens:  imageTokens(u.InputTokensByModality),
		OutputTokens:      u.TotalOutputTokens,
		OutputImageTokens: imageTokens(u.OutputTokensByModality),
		ThoughtTokens:     u.TotalThoughtTokens,
	}
}

func imageTokens(by []modalityTokens) int {
	n := 0
	for _, m := range by {
		if m.Modality == "image" {
			n += m.Tokens
		}
	}
	return n
}

func (r *interaction) err() error {
	switch r.Status {
	case "completed":
		return nil
	case "incomplete":
		return fmt.Errorf("%w%s", ErrIncomplete, r.reasons())
	default:
		return fmt.Errorf("%w: status %q%s", ErrFailed, r.Status, r.reasons())
	}
}

func (r *interaction) reasons() string {
	var b strings.Builder
	for _, e := range r.Errors {
		fmt.Fprintf(&b, ": %s: %s", e.Code, e.Message)
	}
	return b.String()
}
