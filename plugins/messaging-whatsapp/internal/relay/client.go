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
	sourceID string
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

// UserError is returned when the relay rejects a message with a user-facing reason.
type UserError struct {
	Message string
}

func (e *UserError) Error() string { return e.Message }

// Response is the relay's reply.
type Response struct {
	Response    string `json:"response"`
	Responder   string `json:"responder,omitempty"`
	UserMessage string `json:"user_message,omitempty"`
}

// Chat sends a message through the relay and returns the response.
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

	if resp.UserMessage != "" {
		return nil, &UserError{Message: resp.UserMessage}
	}

	if resp.Response == "" {
		return nil, fmt.Errorf("empty response from relay")
	}

	return &resp, nil
}
