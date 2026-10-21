package openai

import (
	"context"
	"fmt"
	"strings"
)

// Request is one call of the Responses API.
type Request struct {
	Model        string
	Instructions string
	Input        string
	// ReasoningEffort is sent as reasoning.effort. Empty leaves the model default.
	ReasoningEffort string
	// MaxOutputTokens caps output plus reasoning tokens. Zero leaves the model default.
	MaxOutputTokens int
}

// Response is the answer of the model.
type Response struct {
	ID    string
	Model string
	// Text joins every output_text part of the answer.
	Text    string
	Usage   Usage
	CostUSD float64
	// Priced is false when the price table holds neither the requested nor the returned
	// model. CostUSD is then 0.
	Priced bool
}

// Usage counts the tokens of one Responses call. InputTokens includes CachedTokens, and
// OutputTokens includes reasoning tokens.
type Usage struct {
	InputTokens  int
	CachedTokens int
	OutputTokens int
}

type responsesRequest struct {
	Model           string     `json:"model"`
	Instructions    string     `json:"instructions,omitempty"`
	Input           string     `json:"input"`
	Reasoning       *reasoning `json:"reasoning,omitempty"`
	MaxOutputTokens int        `json:"max_output_tokens,omitempty"`
	Store           bool       `json:"store"`
}

type reasoning struct {
	Effort string `json:"effort"`
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
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Respond sends req to the Responses API with store set to false. On ErrIncomplete,
// ErrRefused and ErrFailed it still returns the Response, so the caller can record the
// usage and cost of the call.
func (c *Client) Respond(ctx context.Context, req Request) (Response, error) {
	payload := responsesRequest{
		Model:           req.Model,
		Instructions:    req.Instructions,
		Input:           req.Input,
		MaxOutputTokens: req.MaxOutputTokens,
	}
	if req.ReasoningEffort != "" {
		payload.Reasoning = &reasoning{Effort: req.ReasoningEffort}
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
	}
	if p, ok := c.price(req.Model, r.Model); ok {
		resp.CostUSD = resp.Usage.USD(p)
		resp.Priced = true
	}
	var err error
	resp.Text, err = r.text()
	return resp, err
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
