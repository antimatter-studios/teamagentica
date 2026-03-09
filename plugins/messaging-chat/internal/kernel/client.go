package kernel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

type Client struct {
	baseURL      string
	serviceToken string
	httpClient   *http.Client
	debug        bool
}

type ChatRequest struct {
	Message       string            `json:"message"`
	Model         string            `json:"model,omitempty"`
	ImageURLs     []string          `json:"image_urls,omitempty"`
	Conversation  []ConversationMsg `json:"conversation"`
	IsCoordinator bool              `json:"is_coordinator,omitempty"`
	AgentAlias    string            `json:"agent_alias,omitempty"`
}

type ConversationMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatResponse struct {
	Response    string            `json:"response"`
	Model       string            `json:"model"`
	Backend     string            `json:"backend"`
	Usage       *Usage            `json:"usage,omitempty"`
	Attachments []MediaAttachment `json:"attachments,omitempty"`
}

// MediaAttachment is a structured media item returned by an agent.
type MediaAttachment struct {
	MimeType  string `json:"mime_type"`
	ImageData string `json:"image_data"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

func NewClient(baseURL, serviceToken string, debug bool) *Client {
	return &Client{
		baseURL:      baseURL,
		serviceToken: serviceToken,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		debug: debug,
	}
}

// ChatWithAgent sends a chat request to a specific plugin via kernel proxy.
// Pass isCoordinator=true for coordinator calls, and agentAlias for the target agent's alias.
func (c *Client) ChatWithAgent(userID uint, pluginID, model, message string, imageURLs []string, history []ConversationMsg, isCoordinator bool, agentAlias string) (*ChatResponse, error) {
	var conv []ConversationMsg
	conv = append(conv, history...)
	conv = append(conv, ConversationMsg{Role: "user", Content: message})

	reqBody := ChatRequest{
		Message:       message,
		Model:         model,
		ImageURLs:     imageURLs,
		Conversation:  conv,
		IsCoordinator: isCoordinator,
		AgentAlias:    agentAlias,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	url := fmt.Sprintf("%s/api/route/%s/chat", c.baseURL, pluginID)
	if c.debug {
		log.Printf("[kernel] POST %s model=%q history=%d", url, model, len(conv)-1)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.serviceToken)
	if userID != 0 {
		req.Header.Set("X-Teamagentica-User-ID", fmt.Sprintf("%d", userID))
	}

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if c.debug {
		log.Printf("[kernel] response status=%d time=%v", resp.StatusCode, elapsed)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshalling response: %w", err)
	}

	return &chatResp, nil
}
