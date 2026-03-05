package stability

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"time"
)

const baseURL = "https://api.stability.ai/v2beta/stable-image/generate"

// GenerateRequest is the input for image generation.
type GenerateRequest struct {
	Prompt         string `json:"prompt"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
	AspectRatio    string `json:"aspect_ratio,omitempty"` // 1:1, 16:9, 9:16, etc.
	OutputFormat   string `json:"output_format,omitempty"` // png or jpeg
}

// GenerateResult holds the generated image data.
type GenerateResult struct {
	ImageData string `json:"image_data"` // base64-encoded
	MimeType  string `json:"mime_type"`
	Seed      string `json:"seed"`
}

// Client talks to the Stability AI API.
type Client struct {
	apiKey     string
	model      string
	httpClient *http.Client
	debug      bool
}

func NewClient(apiKey, model string, debug bool) *Client {
	return &Client{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		debug: debug,
	}
}

// Generate creates an image from a text prompt via the Stability AI API.
// Uses multipart/form-data with Accept: application/json to get base64 response.
func (c *Client) Generate(req GenerateRequest) (*GenerateResult, error) {
	// Determine endpoint based on model.
	endpoint := baseURL + "/sd3"

	outputFormat := req.OutputFormat
	if outputFormat == "" {
		outputFormat = "png"
	}

	aspectRatio := req.AspectRatio
	if aspectRatio == "" {
		aspectRatio = "1:1"
	}

	// Build multipart form body.
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	writer.WriteField("prompt", req.Prompt)
	writer.WriteField("model", c.model)
	writer.WriteField("mode", "text-to-image")
	writer.WriteField("aspect_ratio", aspectRatio)
	writer.WriteField("output_format", outputFormat)

	if req.NegativePrompt != "" && c.model != "sd3-large-turbo" {
		writer.WriteField("negative_prompt", req.NegativePrompt)
	}

	writer.Close()

	if c.debug {
		log.Printf("[stability] POST %s model=%s prompt=%s", endpoint, c.model, truncate(req.Prompt, 100))
	}

	httpReq, err := http.NewRequest("POST", endpoint, &buf)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if c.debug {
		log.Printf("[stability] response status=%d len=%d", resp.StatusCode, len(respBody))
	}

	if resp.StatusCode != http.StatusOK {
		// Parse error response.
		var errResp struct {
			Name   string   `json:"name"`
			Errors []string `json:"errors"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Name != "" {
			return nil, fmt.Errorf("%s: %v", errResp.Name, errResp.Errors)
		}
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, truncate(string(respBody), 500))
	}

	// Parse JSON response with base64 image.
	var apiResp struct {
		Image        string `json:"image"`
		FinishReason string `json:"finish_reason"`
		Seed         int64  `json:"seed"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if apiResp.Image == "" {
		return nil, fmt.Errorf("no image in response (finish_reason=%s)", apiResp.FinishReason)
	}

	// Validate base64.
	if _, err := base64.StdEncoding.DecodeString(apiResp.Image); err != nil {
		return nil, fmt.Errorf("invalid base64 in response: %w", err)
	}

	mimeType := "image/png"
	if outputFormat == "jpeg" {
		mimeType = "image/jpeg"
	}

	return &GenerateResult{
		ImageData: apiResp.Image,
		MimeType:  mimeType,
		Seed:      fmt.Sprintf("%d", apiResp.Seed),
	}, nil
}

// ListModels fetches available engines from the Stability AI API.
func (c *Client) ListModels() ([]string, bool, error) {
	req, err := http.NewRequest("GET", "https://api.stability.ai/v1/engines/list", nil)
	if err != nil {
		return DefaultModels(), true, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return DefaultModels(), true, fmt.Errorf("list engines: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return DefaultModels(), true, fmt.Errorf("list engines returned %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var engines []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &engines); err != nil {
		return DefaultModels(), true, fmt.Errorf("parse engines: %w", err)
	}

	var models []string
	for _, e := range engines {
		models = append(models, e.ID)
	}

	if len(models) == 0 {
		return DefaultModels(), true, nil
	}

	return models, false, nil
}

func DefaultModels() []string {
	return []string{
		"sd3-medium",
		"sd3-large",
		"sd3-large-turbo",
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
