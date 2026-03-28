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
	sourceID string // e.g. "messaging-telegram"
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

// Response is the relay's immediate reply with a task group ID.
type Response struct {
	TaskGroupID string `json:"task_group_id"`
	UserMessage string `json:"user_message,omitempty"`
}

// Chat sends a message through the relay. Returns a task_group_id immediately.
// The actual response is delivered via relay:progress events.
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

	if resp.TaskGroupID == "" {
		return nil, fmt.Errorf("no task_group_id in relay response")
	}

	return &resp, nil
}
