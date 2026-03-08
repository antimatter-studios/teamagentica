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

const baseURL = "https://seedanceapi.org/v1"

// GenerateRequest is the body for POST /v1/generate.
type GenerateRequest struct {
	Prompt        string   `json:"prompt"`
	AspectRatio   string   `json:"aspect_ratio,omitempty"`
	Resolution    string   `json:"resolution,omitempty"`    // "480p" or "720p"
	Duration      string   `json:"duration,omitempty"`      // "4", "8", or "12"
	GenerateAudio bool     `json:"generate_audio,omitempty"`
	FixedLens     bool     `json:"fixed_lens,omitempty"`
	ImageURLs     []string `json:"image_urls,omitempty"` // max 1 for image-to-video
}

// GenerateResult is returned after submitting a generation request.
type GenerateResult struct {
	TaskID string `json:"task_id"`
}

// StatusResult describes the current state of a generation task.
type StatusResult struct {
	Status         string `json:"status"` // "processing", "completed", "failed"
	VideoURL       string `json:"video_url,omitempty"`
	ConsumedCredits int   `json:"consumed_credits,omitempty"`
	Error          string `json:"error,omitempty"`
}

// Client talks to the Seedance 2.0 API.
type Client struct {
	apiKey     string
	httpClient *http.Client
	debug      bool
}

func NewClient(apiKey string, debug bool) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		debug: debug,
	}
}

// Generate submits a video generation request (text-to-video or image-to-video).
func (c *Client) Generate(req GenerateRequest) (*GenerateResult, error) {
	body := map[string]interface{}{
		"prompt": req.Prompt,
	}

	if req.AspectRatio != "" {
		body["aspect_ratio"] = req.AspectRatio
	}
	if req.Resolution != "" {
		body["resolution"] = req.Resolution
	}
	if req.Duration != "" {
		body["duration"] = req.Duration
	}
	if req.GenerateAudio {
		body["generate_audio"] = true
	}
	if req.FixedLens {
		body["fixed_lens"] = true
	}
	if len(req.ImageURLs) > 0 {
		body["image_urls"] = req.ImageURLs
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := baseURL + "/generate"

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

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("invalid API key")
	}
	if resp.StatusCode == http.StatusPaymentRequired {
		return nil, fmt.Errorf("insufficient credits")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("seedance API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			TaskID string `json:"task_id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if result.Code != 200 {
		return nil, fmt.Errorf("seedance error: %s", result.Message)
	}

	if result.Data.TaskID == "" {
		return nil, fmt.Errorf("no task_id in response")
	}

	return &GenerateResult{TaskID: result.Data.TaskID}, nil
}

// CheckStatus polls for task completion.
func (c *Client) CheckStatus(taskID string) (*StatusResult, error) {
	url := fmt.Sprintf("%s/status?task_id=%s", baseURL, taskID)

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
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			TaskID          string   `json:"task_id"`
			Status          string   `json:"status"` // "SUCCESS", "IN_PROGRESS", "FAILED"
			ConsumedCredits int      `json:"consumed_credits"`
			Response        []string `json:"response"` // video URLs
			ErrorMessage    *string  `json:"error_message"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse status: %w", err)
	}

	sr := &StatusResult{
		ConsumedCredits: result.Data.ConsumedCredits,
	}

	switch result.Data.Status {
	case "SUCCESS":
		sr.Status = "completed"
		if len(result.Data.Response) > 0 {
			sr.VideoURL = result.Data.Response[0]
		}
	case "FAILED":
		sr.Status = "failed"
		if result.Data.ErrorMessage != nil {
			sr.Error = *result.Data.ErrorMessage
		}
	default: // IN_PROGRESS or other
		sr.Status = "processing"
	}

	return sr, nil
}
