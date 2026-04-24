package nanobanana

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const baseURL = "https://generativelanguage.googleapis.com/v1beta"

// GenerateRequest is the input for image generation.
type GenerateRequest struct {
	Prompt      string `json:"prompt"`
	Model       string `json:"model,omitempty"`        // overrides client default
	AspectRatio string `json:"aspect_ratio,omitempty"` // e.g. "1:1", "16:9", "9:16"
}

// GenerateResult holds the generated image data.
type GenerateResult struct {
	ImageData string `json:"image_data"` // base64-encoded PNG
	MimeType  string `json:"mime_type"`
	Text      string `json:"text,omitempty"` // optional text response
}

// Client talks to the Gemini API for Nano Banana image generation.
type Client struct {
	apiKey       string
	defaultModel string
	httpClient   *http.Client
	debug        bool
}

func NewClient(apiKey, defaultModel string, debug bool) *Client {
	return &Client{
		apiKey:       apiKey,
		defaultModel: defaultModel,
		httpClient: &http.Client{
			Timeout: 60 * time.Second, // image gen can take a moment
		},
		debug: debug,
	}
}

// DefaultModel returns the client's configured default model.
func (c *Client) DefaultModel() string {
	return c.defaultModel
}

// Generate creates an image from a text prompt. This is synchronous — returns
// the image data directly (no polling needed unlike video generation).
func (c *Client) Generate(req GenerateRequest) (*GenerateResult, error) {
	// Build the Gemini generateContent request with image response modality.
	body := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{"text": req.Prompt},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"responseModalities": []string{"TEXT", "IMAGE"},
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	model := req.Model
	if model == "" {
		model = c.defaultModel
	}

	url := fmt.Sprintf("%s/models/%s:generateContent", baseURL, model)

	if c.debug {
		log.Printf("[nanobanana] POST %s body=%s", url, string(jsonBody))
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if c.debug {
		// Don't log full base64 data — just the length.
		log.Printf("[nanobanana] response status=%d len=%d", resp.StatusCode, len(respBody))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, truncateBytes(respBody, 500))
	}

	// Parse the response to extract image data.
	var apiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text       string `json:"text,omitempty"`
					InlineData *struct {
						MimeType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData,omitempty"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if len(apiResp.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates in response")
	}

	result := &GenerateResult{}
	for _, part := range apiResp.Candidates[0].Content.Parts {
		if part.InlineData != nil {
			result.ImageData = part.InlineData.Data
			result.MimeType = part.InlineData.MimeType
		}
		if part.Text != "" {
			result.Text = part.Text
		}
	}

	if result.ImageData == "" {
		if result.Text != "" {
			return nil, fmt.Errorf("%s", result.Text)
		}
		return nil, fmt.Errorf("no image data in response")
	}

	return result, nil
}

// ListModels fetches available models from the Gemini API and filters to
// those that support image generation (name contains "image").
func (c *Client) ListModels() ([]string, error) {
	url := fmt.Sprintf("%s/models?key=%s", baseURL, c.apiKey)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list models returned %d: %s", resp.StatusCode, truncateBytes(body, 200))
	}

	var result struct {
		Models []struct {
			Name                       string   `json:"name"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse models: %w", err)
	}

	var models []string
	for _, m := range result.Models {
		for _, method := range m.SupportedGenerationMethods {
			if method == "generateContent" {
				name := m.Name
				if idx := len("models/"); len(name) > idx && name[:idx] == "models/" {
					name = name[idx:]
				}
				if strings.Contains(name, "image") {
					models = append(models, name)
				}
				break
			}
		}
	}

	return models, nil
}

func truncateBytes(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}
