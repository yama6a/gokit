package openai

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"strconv"
)

// ImageRequest asks a GPT image model for new images. Empty fields leave the API default.
type ImageRequest struct {
	Model  string
	Prompt string
	// Size is WIDTHxHEIGHT, for example 1536x1024, or "auto".
	Size    string
	Quality string
	// N is the number of images. Zero asks for one.
	N          int
	Background string
	// OutputFormat is "png", "jpeg" or "webp". The API default is png.
	OutputFormat string
}

// ImageEditRequest asks for new images drawn from the prompt and the reference Images.
type ImageEditRequest struct {
	ImageRequest

	Images []NamedImage
}

// NamedImage is an image file with its file name and MIME type.
type NamedImage struct {
	Name     string
	MimeType string
	Data     []byte
}

// ImageResult holds the decoded images and the usage of one Images call.
type ImageResult struct {
	Images [][]byte
	// MimeType is the type of every image in Images, for example image/png.
	MimeType string
	Usage    ImageUsage
	CostUSD  float64
	// Priced is false when the price table does not hold the model. CostUSD is then 0.
	Priced bool
}

// ImageUsage counts the tokens of one Images call. InputTokens and OutputTokens are the
// sums of their text and image parts.
type ImageUsage struct {
	InputTokens       int
	InputTextTokens   int
	InputImageTokens  int
	OutputTokens      int
	OutputTextTokens  int
	OutputImageTokens int
}

type imageGenerateRequest struct {
	Model        string `json:"model"`
	Prompt       string `json:"prompt"`
	N            int    `json:"n,omitempty"`
	Size         string `json:"size,omitempty"`
	Quality      string `json:"quality,omitempty"`
	Background   string `json:"background,omitempty"`
	OutputFormat string `json:"output_format,omitempty"`
}

type tokenDetails struct {
	TextTokens  int `json:"text_tokens"`
	ImageTokens int `json:"image_tokens"`
}

type imagesResponse struct {
	OutputFormat string `json:"output_format"`
	Data         []struct {
		B64JSON string `json:"b64_json"`
	} `json:"data"`
	Usage struct {
		InputTokens         int           `json:"input_tokens"`
		InputTokensDetails  tokenDetails  `json:"input_tokens_details"`
		OutputTokens        int           `json:"output_tokens"`
		OutputTokensDetails *tokenDetails `json:"output_tokens_details"`
	} `json:"usage"`
}

// GenerateImages calls the Images generations endpoint, which draws from the prompt alone.
func (c *Client) GenerateImages(ctx context.Context, req ImageRequest) (ImageResult, error) {
	payload := imageGenerateRequest{
		Model:        req.Model,
		Prompt:       req.Prompt,
		N:            req.N,
		Size:         req.Size,
		Quality:      req.Quality,
		Background:   req.Background,
		OutputFormat: req.OutputFormat,
	}
	var r imagesResponse
	if err := c.postJSON(ctx, "/images/generations", payload, &r); err != nil {
		return ImageResult{}, err
	}
	return c.imageResult(req, &r)
}

// EditImages calls the Images edits endpoint, which draws on the reference images.
func (c *Client) EditImages(ctx context.Context, req ImageEditRequest) (ImageResult, error) {
	body, contentType := imageEditBody(req)
	var r imagesResponse
	if err := c.post(ctx, "/images/edits", contentType, body, &r); err != nil {
		return ImageResult{}, err
	}
	return c.imageResult(req.ImageRequest, &r)
}

func (c *Client) imageResult(req ImageRequest, r *imagesResponse) (ImageResult, error) {
	res := ImageResult{
		MimeType: "image/" + cmp.Or(r.OutputFormat, req.OutputFormat, "png"),
		Usage: ImageUsage{
			InputTokens:       r.Usage.InputTokens,
			InputTextTokens:   r.Usage.InputTokensDetails.TextTokens,
			InputImageTokens:  r.Usage.InputTokensDetails.ImageTokens,
			OutputTokens:      r.Usage.OutputTokens,
			OutputImageTokens: r.Usage.OutputTokens,
		},
	}
	if d := r.Usage.OutputTokensDetails; d != nil {
		res.Usage.OutputTextTokens = d.TextTokens
		res.Usage.OutputImageTokens = d.ImageTokens
	}
	if p, ok := c.price(req.Model); ok {
		res.CostUSD = res.Usage.USD(p)
		res.Priced = true
	}

	res.Images = make([][]byte, 0, len(r.Data))
	for i, d := range r.Data {
		img, err := base64.StdEncoding.DecodeString(d.B64JSON)
		if err != nil {
			return res, fmt.Errorf("openai: decode image %d: %w", i, err)
		}
		res.Images = append(res.Images, img)
	}
	if want := max(req.N, 1); len(res.Images) != want {
		return res, fmt.Errorf("%w: got %d, want %d", ErrImageCount, len(res.Images), want)
	}
	return res, nil
}

// imageEditBody ignores write errors: multipart.Writer fails only when its writer does,
// and bytes.Buffer never fails.
func imageEditBody(req ImageEditRequest) ([]byte, string) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	n := ""
	if req.N > 0 {
		n = strconv.Itoa(req.N)
	}
	fields := []struct{ name, value string }{
		{"model", req.Model},
		{"prompt", req.Prompt},
		{"n", n},
		{"size", req.Size},
		{"quality", req.Quality},
		{"background", req.Background},
		{"output_format", req.OutputFormat},
	}
	for _, f := range fields {
		if f.value != "" {
			_ = w.WriteField(f.name, f.value)
		}
	}
	for _, img := range req.Images {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", multipart.FileContentDisposition("image[]", img.Name))
		h.Set("Content-Type", cmp.Or(img.MimeType, "application/octet-stream"))
		part, _ := w.CreatePart(h)
		_, _ = part.Write(img.Data)
	}
	_ = w.Close()
	return buf.Bytes(), w.FormDataContentType()
}
