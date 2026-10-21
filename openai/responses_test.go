package openai

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

type imageReview struct {
	Approved bool     `json:"approved"`
	Problems []string `json:"problems"`
}

func imageReviewSchema() *Schema {
	return &Schema{Name: "image_review", Schema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"approved": map[string]any{"type": "boolean"},
			"problems": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required":             []string{"approved", "problems"},
		"additionalProperties": false,
	}}
}

func imageReviewRequest() Request {
	return Request{
		Model:           "gpt-5-mini",
		Instructions:    "You review title images.",
		ReasoningEffort: "medium",
		Messages: []Message{
			{Role: "developer", Content: []Part{Text("Answer in English.")}},
			{Role: "user", Content: []Part{
				Text("IMAGE TO REVIEW"),
				Image([]byte("\x89PNG"), "image/png", "low"),
				Text("REFERENCE"),
				ImageURL("https://example.com/seb-logo.png", ""),
			}},
		},
		Schema: imageReviewSchema(),
	}
}

func TestClient_RespondJSON(t *testing.T) {
	t.Parallel()
	c, reqs := newTestClient(t, http.StatusOK, fixture(t, "responses_json.json"))

	var got imageReview
	resp, err := c.RespondJSON(t.Context(), imageReviewRequest(), &got)
	require.NoError(t, err)
	require.Equal(t, imageReview{Problems: []string{"The SEB logo is cut off."}}, got)
	require.Equal(t, Response{
		ID:              "resp_68f7b1000d5f8190e9a4f6d7a8192b3c0aa1b2c3d4e5f6a7",
		Model:           "gpt-5-mini-2025-08-07",
		Text:            `{"approved":false,"problems":["The SEB logo is cut off."]}`,
		Usage:           Usage{InputTokens: 2400, OutputTokens: 900},
		ReasoningTokens: 860,
		CostUSD:         0.0024,
		Priced:          true,
	}, resp)

	r := <-reqs
	require.Equal(t, "/v1/responses", r.Path)
	require.JSONEq(t, string(fixture(t, "request_messages.json")), string(r.Body))
}

func TestClient_RespondJSONErrors(t *testing.T) {
	t.Parallel()

	t.Run("refusal keeps usage", func(t *testing.T) {
		t.Parallel()
		c, _ := newTestClient(t, http.StatusOK, fixture(t, "responses_refusal.json"))

		var out imageReview
		resp, err := c.RespondJSON(t.Context(), imageReviewRequest(), &out)
		require.ErrorIs(t, err, ErrRefused)
		require.Equal(t, Response{
			ID:      "resp_68f7a2000a2c8190b6f1c3a4d5e6f7080aa1b2c3d4e5f6a7",
			Model:   "gpt-5-mini-2025-08-07",
			Usage:   Usage{InputTokens: 120, OutputTokens: 12},
			CostUSD: 0.000054,
			Priced:  true,
		}, resp)
		require.Equal(t, imageReview{}, out)
	})

	t.Run("answer is not json", func(t *testing.T) {
		t.Parallel()
		c, _ := newTestClient(t, http.StatusOK, fixture(t, "responses_completed.json"))

		var out imageReview
		resp, err := c.RespondJSON(t.Context(), imageReviewRequest(), &out)
		_, isSyntaxErr := errors.AsType[*json.SyntaxError](err)
		require.True(t, isSyntaxErr)
		require.Equal(t, "Autumn light on the fjord. Coffee first, then the hike.", resp.Text)
		require.True(t, resp.Priced)
	})
}

func TestClient_RespondInvalidRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(*Client) error
	}{
		{
			name: "input and messages",
			call: func(c *Client) error {
				_, err := c.Respond(t.Context(), Request{
					Model:    "gpt-5-mini",
					Input:    "Hi",
					Messages: []Message{{Role: "user", Content: []Part{Text("Hi")}}},
				})
				return err
			},
		},
		{
			name: "RespondJSON without schema",
			call: func(c *Client) error {
				var out imageReview
				_, err := c.RespondJSON(t.Context(), Request{Model: "gpt-5-mini", Input: "Hi"}, &out)
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, reqs := newTestClient(t, http.StatusOK, fixture(t, "responses_completed.json"))

			require.ErrorIs(t, tt.call(c), ErrInvalidRequest)
			require.Empty(t, reqs)
		})
	}
}

func TestClient_RespondSchemaWithInput(t *testing.T) {
	t.Parallel()
	c, reqs := newTestClient(t, http.StatusOK, fixture(t, "responses_json.json"))

	_, err := c.Respond(t.Context(), Request{Model: "gpt-5-mini", Input: "Review it.", Schema: &Schema{Name: "s", Schema: map[string]any{"type": "object"}}})
	require.NoError(t, err)
	require.JSONEq(t, `{
		"model": "gpt-5-mini",
		"input": "Review it.",
		"text": {"format": {"type": "json_schema", "name": "s", "schema": {"type": "object"}, "strict": true}},
		"store": false
	}`, string((<-reqs).Body))
}

func TestParts(t *testing.T) {
	t.Parallel()

	require.Equal(t, []Part{
		{Type: "input_text", Text: "Hi"},
		{Type: "input_image", ImageURL: "data:image/jpeg;base64,AQID", Detail: "high"},
		{Type: "input_image", ImageURL: "https://example.com/a.png", Detail: "auto"},
	}, []Part{
		Text("Hi"),
		Image([]byte{1, 2, 3}, "image/jpeg", "high"),
		ImageURL("https://example.com/a.png", "auto"),
	})
}

func TestClient_RespondSchemaNotEncodable(t *testing.T) {
	t.Parallel()
	c, reqs := newTestClient(t, http.StatusOK, fixture(t, "responses_json.json"))

	_, err := c.Respond(t.Context(), Request{Model: "gpt-5-mini", Input: "Hi", Schema: &Schema{Name: "s", Schema: func() {}}})
	_, isTypeErr := errors.AsType[*json.UnsupportedTypeError](err)
	require.True(t, isTypeErr)
	require.Empty(t, reqs)
}
