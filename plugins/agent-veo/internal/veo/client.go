package veo

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

// GenerateRequest is the body for POST /generate.
type GenerateRequest struct {
	Prompt        string `json:"prompt"`
	AspectRatio   string `json:"aspect_ratio,omitempty"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
}

// GenerateResult is returned after submitting a generation request.
type GenerateResult struct {
	OperationName string `json:"operation_name"`
}

// StatusResult describes the current state of a generation task.
type StatusResult struct {
	Done     bool   `json:"done"`
	VideoURI string `json:"video_uri,omitempty"`
	Error    string `json:"error,omitempty"`
}

// UsageMetadata from the Veo API response.
type UsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// Client talks to the Google Veo API via Gemini endpoints.
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
			Timeout: 30 * time.Second,
		},
		debug: debug,
	}
}

// Generate submits a video generation request. Returns the operation name for polling.
func (c *Client) Generate(req GenerateRequest) (*GenerateResult, error) {
	aspectRatio := req.AspectRatio
	if aspectRatio == "" {
		aspectRatio = "16:9"
	}

	body := map[string]interface{}{
		"instances": []map[string]interface{}{
			{"prompt": req.Prompt},
		},
		"parameters": map[string]interface{}{
			"aspectRatio": aspectRatio,
		},
	}

	if req.NegativePrompt != "" {
		body["parameters"].(map[string]interface{})["negativePrompt"] = req.NegativePrompt
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:predictLongRunning", baseURL, c.model)

	if c.debug {
		log.Printf("[veo] POST %s body=%s", url, string(jsonBody))
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
		log.Printf("[veo] response status=%d body=%s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("veo API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if result.Name == "" {
		return nil, fmt.Errorf("no operation name in response")
	}

	return &GenerateResult{OperationName: result.Name}, nil
}

// CheckStatus polls the operation for completion.
func (c *Client) CheckStatus(operationName string) (*StatusResult, error) {
	url := fmt.Sprintf("%s/%s", baseURL, operationName)

	if c.debug {
		log.Printf("[veo] GET %s", url)
	}

	httpReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("x-goog-api-key", c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if c.debug {
		log.Printf("[veo] status response=%s", string(respBody))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, fmt.Errorf("parse status: %w", err)
	}

	result := &StatusResult{}

	// Check if done.
	if done, ok := raw["done"].(bool); ok {
		result.Done = done
	}

	// Check for error.
	if errObj, ok := raw["error"].(map[string]interface{}); ok {
		if msg, ok := errObj["message"].(string); ok {
			result.Error = msg
			result.Done = true
		}
	}

	// Extract video URI from completed response.
	if result.Done && result.Error == "" {
		result.VideoURI = extractVideoURI(raw)
	}

	return result, nil
}

// ListModels returns available Veo models.
func (c *Client) ListModels() ([]string, error) {
	url := fmt.Sprintf("%s/models?key=%s", baseURL, c.apiKey)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list models returned %d: %s", resp.StatusCode, string(body))
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
			if method == "predictLongRunning" {
				// Extract short model name from "models/veo-xxx".
				name := m.Name
				if idx := strings.LastIndex(name, "/"); idx >= 0 {
					name = name[idx+1:]
				}
				models = append(models, name)
				break
			}
		}
	}

	return models, nil
}

// extractVideoURI digs into the nested response to find the video URI.
func extractVideoURI(raw map[string]interface{}) string {
	// Path: response.generateVideoResponse.generatedSamples[0].video.uri
	response, ok := raw["response"].(map[string]interface{})
	if !ok {
		return ""
	}
	genResp, ok := response["generateVideoResponse"].(map[string]interface{})
	if !ok {
		return ""
	}
	samples, ok := genResp["generatedSamples"].([]interface{})
	if !ok || len(samples) == 0 {
		return ""
	}
	sample, ok := samples[0].(map[string]interface{})
	if !ok {
		return ""
	}
	video, ok := sample["video"].(map[string]interface{})
	if !ok {
		return ""
	}
	uri, _ := video["uri"].(string)
	return uri
}
