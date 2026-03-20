package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

// Client sends messages to the infra-agent-relay plugin via the kernel route.
type Client struct {
	sdk      *pluginsdk.Client
	sourceID string // e.g. "messaging-chat"
}

// NewClient creates a relay client that routes through the kernel.
func NewClient(sdk *pluginsdk.Client, sourceID string) *Client {
	return &Client{sdk: sdk, sourceID: sourceID}
}

// Request is the message envelope sent to the relay.
type Request struct {
	SourcePlugin string   `json:"source_plugin"`
	ChannelID    string   `json:"channel_id"`
	Message      string   `json:"message"`
	ImageURLs    []string `json:"image_urls,omitempty"`
}

// Usage holds token usage from the agent.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	CachedTokens     int `json:"cached_tokens,omitempty"`
}

// Attachment is a media item returned by the agent.
type Attachment struct {
	MimeType  string `json:"mime_type"`
	ImageData string `json:"image_data"`
}

// Response is the relay's reply, including agent metadata.
type Response struct {
	Response    string       `json:"response"`
	Responder   string       `json:"responder,omitempty"`
	Model       string       `json:"model,omitempty"`
	Backend     string       `json:"backend,omitempty"`
	Usage       *Usage       `json:"usage,omitempty"`
	CostUSD     float64      `json:"cost_usd,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Chat sends a message through the relay and returns the full response.
func (c *Client) Chat(channelID, message string, imageURLs []string) (*Response, error) {
	req := Request{
		SourcePlugin: c.sourceID,
		ChannelID:    channelID,
		Message:      message,
		ImageURLs:    imageURLs,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	respBody, err := c.sdk.RouteToPlugin(context.Background(), "infra-agent-relay", "POST", "/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("relay: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	if resp.Response == "" {
		return nil, fmt.Errorf("empty response from relay")
	}

	return &resp, nil
}
