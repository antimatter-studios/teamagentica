package nanobanana

import (
	"bytes"
	"encoding/base64"
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
	Prompt      string   `json:"prompt"`
	Model       string   `json:"model,omitempty"`        // overrides client default
	AspectRatio string   `json:"aspect_ratio,omitempty"` // e.g. "1:1", "16:9", "9:16"
	ImageURLs   []string `json:"image_urls,omitempty"`   // reference images for image-to-image / editing
}

// GeneratedImage is a single inline image returned by the model.
type GeneratedImage struct {
	Data     string `json:"data"` // base64-encoded
	MimeType string `json:"mime_type"`
}

// GenerateResult holds every inline image the model emitted across all
// candidates and parts, plus any concatenated text response. Gemini can
// return multiple candidates *and* multiple inlineData parts within a
// single candidate — both axes must be iterated or images are lost.
type GenerateResult struct {
	Images []GeneratedImage `json:"images"`
	Text   string           `json:"text,omitempty"`
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

// Generate creates an image from a text prompt. If the model returns a
// text-only response (no inline image), retries once with an amplified prompt
// — this happens occasionally on gemini-*-flash-image even with the system
// instruction in place.
func (c *Client) Generate(req GenerateRequest) (*GenerateResult, error) {
	result, err := c.generateOnce(req)
	if err == nil {
		return result, nil
	}
	// Retry once when the failure looks like a text-only acknowledgement.
	// The error message in that path is the raw model text (client.go wraps
	// result.Text into err); retry path stamps the prompt with a stronger
	// imperative so the model is more likely to produce an image.
	retryReq := req
	retryReq.Prompt = "Generate a detailed image matching this description. Output only the image. Description: " + req.Prompt
	if c.debug {
		log.Printf("[nanobanana] retrying with amplified prompt after: %v", err)
	}
	result, retryErr := c.generateOnce(retryReq)
	if retryErr == nil {
		return result, nil
	}
	return nil, err
}

func (c *Client) generateOnce(req GenerateRequest) (*GenerateResult, error) {
	// Reference images go before the text part so the model treats the prompt
	// as an instruction about the attached images (edit / restyle / compose).
	parts := make([]map[string]interface{}, 0, len(req.ImageURLs)+1)
	for _, u := range req.ImageURLs {
		mimeType, data, err := c.downloadImage(u)
		if err != nil {
			return nil, fmt.Errorf("fetch input image %s: %w", u, err)
		}
		parts = append(parts, map[string]interface{}{
			"inlineData": map[string]interface{}{
				"mimeType": mimeType,
				"data":     data,
			},
		})
	}
	parts = append(parts, map[string]interface{}{"text": req.Prompt})

	// System instruction steers the model away from conversational text-only
	// replies ("Sounds like a vibrant scene! Here's an image of…") and toward
	// actually emitting inline image data. Without this, gemini-*-flash-image
	// occasionally acknowledges the request with text only and produces no image.
	// When reference images are provided, we also force identity preservation:
	// flash-image otherwise tends to generate a plausibly-similar stranger.
	sysInstruction := "You are an image generation tool. For every user message, produce an inline image that matches their description. Do not reply with conversational preamble or acknowledgement. If you cannot generate an image for any reason, say so explicitly and briefly."
	if len(req.ImageURLs) > 0 {
		sysInstruction = "You are an image editing tool. The user has attached one or more reference images followed by an instruction. PRESERVE THE IDENTITY of any person in the reference image — same face, same skin tone, same hair, same clothing unless the instruction explicitly asks to change them. Treat the reference image as the subject to edit/restyle/recompose, not as inspiration for a new image. Produce an inline image. Do not reply with conversational preamble. If you cannot comply, say so briefly."
	}
	body := map[string]interface{}{
		"systemInstruction": map[string]interface{}{
			"parts": []map[string]interface{}{
				{"text": sysInstruction},
			},
		},
		"contents": []map[string]interface{}{
			{"parts": parts},
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
	var textParts []string
	for _, cand := range apiResp.Candidates {
		for _, part := range cand.Content.Parts {
			if part.InlineData != nil {
				result.Images = append(result.Images, GeneratedImage{
					Data:     part.InlineData.Data,
					MimeType: part.InlineData.MimeType,
				})
			}
			if part.Text != "" {
				textParts = append(textParts, part.Text)
			}
		}
	}
	result.Text = strings.Join(textParts, "\n")

	if len(result.Images) == 0 {
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

// downloadImage fetches a URL and returns the detected mime type plus
// base64-encoded bytes, ready to embed as a Gemini inlineData part.
func (c *Client) downloadImage(imageURL string) (string, string, error) {
	resp, err := c.httpClient.Get(imageURL)
	if err != nil {
		return "", "", fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("read: %w", err)
	}

	mimeType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(mimeType, "image/") {
		mimeType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(mimeType, "image/") {
		mimeType = "image/jpeg"
	}

	if c.debug {
		log.Printf("[nanobanana] downloaded input image: %s (%s, %d bytes)", imageURL, mimeType, len(data))
	}
	return mimeType, base64.StdEncoding.EncodeToString(data), nil
}

func truncateBytes(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}
