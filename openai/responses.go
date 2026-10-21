package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// Request is one call of the Responses API.
type Request struct {
	Model        string
	Instructions string
	// Input is the input as one text. Leave it empty when Messages is set.
	Input string
	// Messages is the input as messages with text and image parts. It replaces Input.
	Messages []Message
	// ReasoningEffort is sent as reasoning.effort. Empty leaves the model default.
	ReasoningEffort string
	// MaxOutputTokens caps output plus reasoning tokens. Zero leaves the model default.
	MaxOutputTokens int
	// Schema, when set, makes the model answer with JSON that matches it.
	Schema *Schema
}

// Message is one input message. Role is "developer", "user" or "assistant".
type Message struct {
	Role    string `json:"role"`
	Content []Part `json:"content"`
}

// Part is a text or an image of a message. Build it with Text, Image or ImageURL.
type Part struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// Text returns a text part.
func Text(s string) Part {
	return Part{Type: "input_text", Text: s}
}

// Image returns an image part that holds data inline as a data URL. detail is "low",
// "high", "auto" or empty for the API default.
func Image(data []byte, mimeType, detail string) Part {
	return ImageURL("data:"+mimeType+";base64,"+base64.StdEncoding.EncodeToString(data), detail)
}

// ImageURL returns an image part that the API fetches from url.
func ImageURL(url, detail string) Part {
	return Part{Type: "input_image", ImageURL: url, Detail: detail}
}

// Schema is a strict JSON schema for structured output. Every object in it must list all
// its properties as required and set additionalProperties to false.
type Schema struct {
	Name   string
	Schema any
}

// Response is the answer of the model.
type Response struct {
	ID    string
	Model string
	// Text joins every output_text part of the answer.
	Text  string
	Usage Usage
	// ReasoningTokens is the part of Usage.OutputTokens spent on reasoning. It is not in
	// Usage because callers convert Usage to their own struct types.
	ReasoningTokens int
	CostUSD         float64
	// Priced is false when the price table holds neither the requested nor the returned
	// model. CostUSD is then 0.
	Priced bool
}

// Usage counts the tokens of one Responses call. InputTokens includes CachedTokens, and
// OutputTokens includes Response.ReasoningTokens.
type Usage struct {
	InputTokens  int
	CachedTokens int
	OutputTokens int
}

type responsesRequest struct {
	Model           string     `json:"model"`
	Instructions    string     `json:"instructions,omitempty"`
	Input           any        `json:"input"`
	Reasoning       *reasoning `json:"reasoning,omitempty"`
	MaxOutputTokens int        `json:"max_output_tokens,omitempty"`
	Text            *textParam `json:"text,omitempty"`
	Store           bool       `json:"store"`
}

type reasoning struct {
	Effort string `json:"effort"`
}

type textParam struct {
	Format textFormat `json:"format"`
}

type textFormat struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Schema any    `json:"schema"`
	Strict bool   `json:"strict"`
}

type responsesResponse struct {
	ID     string `json:"id"`
	Model  string `json:"model"`
	Status string `json:"status"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		} `json:"content"`
	} `json:"output"`
	Usage struct {
		InputTokens        int `json:"input_tokens"`
		InputTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		OutputTokens        int `json:"output_tokens"`
		OutputTokensDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
}

// Respond sends req to the Responses API with store set to false. On ErrIncomplete,
// ErrRefused and ErrFailed it still returns the Response, so the caller can record the
// usage and cost of the call.
func (c *Client) Respond(ctx context.Context, req Request) (Response, error) {
	payload, err := newResponsesRequest(req)
	if err != nil {
		return Response{}, err
	}

	var r responsesResponse
	if err := c.postJSON(ctx, "/responses", payload, &r); err != nil {
		return Response{}, err
	}

	resp := Response{
		ID:    r.ID,
		Model: r.Model,
		Usage: Usage{
			InputTokens:  r.Usage.InputTokens,
			CachedTokens: r.Usage.InputTokensDetails.CachedTokens,
			OutputTokens: r.Usage.OutputTokens,
		},
		ReasoningTokens: r.Usage.OutputTokensDetails.ReasoningTokens,
	}
	if p, ok := c.price(req.Model, r.Model); ok {
		resp.CostUSD = resp.Usage.USD(p)
		resp.Priced = true
	}
	resp.Text, err = r.text()
	return resp, err
}

// RespondJSON sends req and decodes the answer into out. req.Schema must be set and
// describe out. On an error after the call it still returns the Response, as Respond does.
func (c *Client) RespondJSON(ctx context.Context, req Request, out any) (Response, error) {
	if req.Schema == nil {
		return Response{}, fmt.Errorf("%w: RespondJSON needs a Schema", ErrInvalidRequest)
	}
	resp, err := c.Respond(ctx, req)
	if err != nil {
		return resp, err
	}
	if err := json.Unmarshal([]byte(resp.Text), out); err != nil {
		return resp, fmt.Errorf("openai: decode %s answer: %w", req.Schema.Name, err)
	}
	return resp, nil
}

func newResponsesRequest(req Request) (responsesRequest, error) {
	payload := responsesRequest{
		Model:           req.Model,
		Instructions:    req.Instructions,
		Input:           req.Input,
		MaxOutputTokens: req.MaxOutputTokens,
	}
	if len(req.Messages) > 0 {
		if req.Input != "" {
			return responsesRequest{}, fmt.Errorf("%w: set Input or Messages, not both", ErrInvalidRequest)
		}
		payload.Input = req.Messages
	}
	if req.ReasoningEffort != "" {
		payload.Reasoning = &reasoning{Effort: req.ReasoningEffort}
	}
	if req.Schema != nil {
		payload.Text = &textParam{Format: textFormat{
			Type:   "json_schema",
			Name:   req.Schema.Name,
			Schema: req.Schema.Schema,
			Strict: true,
		}}
	}
	return payload, nil
}

func (r *responsesResponse) text() (string, error) {
	text, refusal, refused := r.output()
	switch {
	case refused:
		return "", fmt.Errorf("%w: %s", ErrRefused, refusal)
	case r.Status == "completed":
		return text, nil
	case r.Status == "incomplete" && r.IncompleteDetails != nil:
		return text, fmt.Errorf("%w: %s", ErrIncomplete, r.IncompleteDetails.Reason)
	case r.Error != nil:
		return "", fmt.Errorf("%w: %s: %s", ErrFailed, r.Error.Code, r.Error.Message)
	default:
		return "", fmt.Errorf("%w: status %q", ErrFailed, r.Status)
	}
}

func (r *responsesResponse) output() (text, refusal string, refused bool) {
	var b strings.Builder
	for _, item := range r.Output {
		if item.Type != "message" {
			continue
		}
		for _, part := range item.Content {
			switch part.Type {
			case "output_text":
				b.WriteString(part.Text)
			case "refusal":
				return "", part.Refusal, true
			}
		}
	}
	return b.String(), "", false
}
