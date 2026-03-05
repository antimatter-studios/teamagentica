package gemini

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

// Message is the standard role+content pair used by the handler layer.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Usage holds token counts from the API response.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CachedTokens     int `json:"cached_tokens"`
}

// ChatResponse is the parsed result of a generateContent call.
type ChatResponse struct {
	Content string
	Usage   Usage
	Headers http.Header
}

// part is a piece of content in the Gemini API.
type part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inlineData,omitempty"`
}

// inlineData holds base64-encoded image data for the Gemini API.
type inlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// content is a message in the Gemini API.
type content struct {
	Role  string `json:"role"`
	Parts []part `json:"parts"`
}

// generateRequest is the request body for generateContent.
type generateRequest struct {
	Contents          []content  `json:"contents"`
	SystemInstruction *content   `json:"systemInstruction,omitempty"`
}

// Client talks to the Google Gemini API.
type Client struct {
	apiKey     string
	httpClient *http.Client
	debug      bool
}

func NewClient(apiKey string, debug bool) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		debug: debug,
	}
}

// ChatCompletion sends a chat request to the Gemini generateContent endpoint.
// imageURLs are attached as inline image parts to the last user message.
func (c *Client) ChatCompletion(model string, messages []Message, imageURLs ...string) (*ChatResponse, error) {
	// Convert messages to Gemini format.
	// Gemini uses "user" and "model" roles (not "assistant").
	// System messages become systemInstruction.
	var contents []content
	var systemInstruction *content

	for _, m := range messages {
		switch m.Role {
		case "system":
			systemInstruction = &content{
				Parts: []part{{Text: m.Content}},
			}
		case "assistant":
			contents = append(contents, content{
				Role:  "model",
				Parts: []part{{Text: m.Content}},
			})
		default: // "user" and anything else
			contents = append(contents, content{
				Role:  "user",
				Parts: []part{{Text: m.Content}},
			})
		}
	}

	// Attach images to the last user message.
	if len(imageURLs) > 0 && len(contents) > 0 {
		last := &contents[len(contents)-1]
		if last.Role == "user" {
			for _, imgURL := range imageURLs {
				imgPart, err := c.downloadImagePart(imgURL)
				if err != nil {
					log.Printf("[gemini] failed to download image %s: %v", imgURL, err)
					continue
				}
				last.Parts = append(last.Parts, *imgPart)
			}
		}
	}

	reqBody := generateRequest{
		Contents:          contents,
		SystemInstruction: systemInstruction,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent", baseURL, model)

	if c.debug {
		log.Printf("[gemini] POST %s body=%s", url, string(jsonBody))
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
		log.Printf("[gemini] response status=%d body=%s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
			CachedContentTokenCount int `json:"cachedContentTokenCount"`
		} `json:"usageMetadata"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	responseText := ""
	if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
		responseText = result.Candidates[0].Content.Parts[0].Text
	}

	return &ChatResponse{
		Content: responseText,
		Usage: Usage{
			PromptTokens:     result.UsageMetadata.PromptTokenCount,
			CompletionTokens: result.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      result.UsageMetadata.TotalTokenCount,
			CachedTokens:     result.UsageMetadata.CachedContentTokenCount,
		},
		Headers: resp.Header,
	}, nil
}

// downloadImagePart fetches an image URL and returns it as an inlineData part.
func (c *Client) downloadImagePart(imageURL string) (*part, error) {
	resp, err := c.httpClient.Get(imageURL)
	if err != nil {
		return nil, fmt.Errorf("fetch image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image fetch returned %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}

	// Telegram returns application/octet-stream; detect from content instead.
	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(mimeType, "image/") {
		mimeType = "image/jpeg"
	}

	if c.debug {
		log.Printf("[gemini] downloaded image: %s (%s, %d bytes)", imageURL, mimeType, len(data))
	}

	return &part{
		InlineData: &inlineData{
			MimeType: mimeType,
			Data:     base64.StdEncoding.EncodeToString(data),
		},
	}, nil
}

// ListModels returns available Gemini models that support generateContent.
func (c *Client) ListModels() ([]string, bool, error) {
	url := fmt.Sprintf("%s/models?key=%s", baseURL, c.apiKey)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return DefaultModels(), true, fmt.Errorf("list models: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return DefaultModels(), true, fmt.Errorf("list models returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Models []struct {
			Name                       string   `json:"name"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return DefaultModels(), true, fmt.Errorf("parse models: %w", err)
	}

	var models []string
	for _, m := range result.Models {
		for _, method := range m.SupportedGenerationMethods {
			if method == "generateContent" {
				name := m.Name
				if idx := strings.LastIndex(name, "/"); idx >= 0 {
					name = name[idx+1:]
				}
				// Only include gemini models, skip embedding/vision-only etc.
				if strings.HasPrefix(name, "gemini-") {
					models = append(models, name)
				}
				break
			}
		}
	}

	if len(models) == 0 {
		return DefaultModels(), true, nil
	}

	return models, false, nil
}

func DefaultModels() []string {
	return []string{
		"gemini-2.5-flash",
		"gemini-2.5-pro",
		"gemini-2.0-flash",
		"gemini-2.0-flash-lite",
	}
}
