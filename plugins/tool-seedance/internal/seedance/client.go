package seedance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

const baseURL = "https://api.seedance.ai"

// GenerateRequest is the body for POST /generate.
type GenerateRequest struct {
	Prompt         string `json:"prompt"`
	AspectRatio    string `json:"aspect_ratio,omitempty"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
	Duration       int    `json:"duration,omitempty"` // 4 or 8 seconds
}

// GenerateResult is returned after submitting a generation request.
type GenerateResult struct {
	TaskID string `json:"task_id"`
}

// StatusResult describes the current state of a generation task.
type StatusResult struct {
	Status   string `json:"status"` // "pending", "processing", "completed", "failed"
	VideoURL string `json:"video_url,omitempty"`
	Duration int    `json:"duration,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Client talks to the Seedance API.
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

// Generate submits a text-to-video generation request.
func (c *Client) Generate(req GenerateRequest) (*GenerateResult, error) {
	aspectRatio := req.AspectRatio
	if aspectRatio == "" {
		aspectRatio = "16:9"
	}
	duration := req.Duration
	if duration == 0 {
		duration = 4
	}

	body := map[string]interface{}{
		"model":        c.model,
		"prompt":       req.Prompt,
		"aspect_ratio": aspectRatio,
		"duration":     duration,
	}

	if req.NegativePrompt != "" {
		body["negative_prompt"] = req.NegativePrompt
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := baseURL + "/v2/generate/text"

	if c.debug {
		log.Printf("[seedance] POST %s body=%s", url, string(jsonBody))
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if c.debug {
		log.Printf("[seedance] response status=%d body=%s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("seedance API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("seedance error [%s]: %s", result.Error.Code, result.Error.Message)
	}

	if result.Data.TaskID == "" {
		return nil, fmt.Errorf("no task_id in response")
	}

	return &GenerateResult{TaskID: result.Data.TaskID}, nil
}

// CheckStatus polls for task completion.
func (c *Client) CheckStatus(taskID string) (*StatusResult, error) {
	url := fmt.Sprintf("%s/v2/tasks/%s", baseURL, taskID)

	if c.debug {
		log.Printf("[seedance] GET %s", url)
	}

	httpReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if c.debug {
		log.Printf("[seedance] status response=%s", string(respBody))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			Status   string `json:"status"`
			VideoURL string `json:"video_url"`
			Duration int    `json:"duration"`
		} `json:"data"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse status: %w", err)
	}

	sr := &StatusResult{
		Status:   result.Data.Status,
		VideoURL: result.Data.VideoURL,
		Duration: result.Data.Duration,
	}

	if result.Error != nil {
		sr.Error = result.Error.Message
		sr.Status = "failed"
	}

	return sr, nil
}
