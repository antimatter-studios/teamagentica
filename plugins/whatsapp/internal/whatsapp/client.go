package whatsapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

const graphAPIBase = "https://graph.facebook.com/v21.0"

// Client talks to the WhatsApp Business Cloud API.
type Client struct {
	accessToken   string
	phoneNumberID string
	httpClient    *http.Client
	debug         bool
}

// NewClient creates a new WhatsApp Cloud API client.
func NewClient(accessToken, phoneNumberID string, debug bool) *Client {
	return &Client{
		accessToken:   accessToken,
		phoneNumberID: phoneNumberID,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		debug:         debug,
	}
}

// SendText sends a text message to a WhatsApp user.
func (c *Client) SendText(to, text string) error {
	payload := map[string]interface{}{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "text",
		"text": map[string]string{
			"body": text,
		},
	}
	return c.sendMessage(payload)
}

// MarkRead marks a message as read (blue ticks).
func (c *Client) MarkRead(messageID string) {
	payload := map[string]interface{}{
		"messaging_product": "whatsapp",
		"status":            "read",
		"message_id":        messageID,
	}
	// Fire and forget.
	if err := c.sendMessage(payload); err != nil {
		log.Printf("[whatsapp] failed to mark read: %v", err)
	}
}

func (c *Client) sendMessage(payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	url := fmt.Sprintf("%s/%s/messages", graphAPIBase, c.phoneNumberID)

	if c.debug {
		log.Printf("[whatsapp] POST %s body=%s", url, string(body))
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.accessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if c.debug {
		log.Printf("[whatsapp] response status=%d body=%s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("API returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// --- Webhook payload types ---

// WebhookPayload is the top-level webhook notification from Meta.
type WebhookPayload struct {
	Object string  `json:"object"`
	Entry  []Entry `json:"entry"`
}

// Entry is one entry in the webhook notification.
type Entry struct {
	ID      string   `json:"id"`
	Changes []Change `json:"changes"`
}

// Change represents a single change in the webhook.
type Change struct {
	Value Value  `json:"value"`
	Field string `json:"field"`
}

// Value holds the message data.
type Value struct {
	MessagingProduct string    `json:"messaging_product"`
	Metadata         Metadata  `json:"metadata"`
	Contacts         []Contact `json:"contacts"`
	Messages         []Message `json:"messages"`
	Statuses         []Status  `json:"statuses"`
}

// Metadata about the WhatsApp Business Account.
type Metadata struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	PhoneNumberID      string `json:"phone_number_id"`
}

// Contact info from the sender.
type Contact struct {
	Profile Profile `json:"profile"`
	WaID    string  `json:"wa_id"`
}

// Profile of the sender.
type Profile struct {
	Name string `json:"name"`
}

// Message is an incoming WhatsApp message.
type Message struct {
	From      string    `json:"from"`
	ID        string    `json:"id"`
	Timestamp string    `json:"timestamp"`
	Type      string    `json:"type"` // "text", "image", "location", "contacts", etc.
	Text      *TextBody `json:"text,omitempty"`
	Location  *Location `json:"location,omitempty"`
	Image     *Media    `json:"image,omitempty"`
	Audio     *Media    `json:"audio,omitempty"`
	Video     *Media    `json:"video,omitempty"`
	Document  *Media    `json:"document,omitempty"`
	Contacts  []VCard   `json:"contacts,omitempty"`
}

// TextBody holds the text content.
type TextBody struct {
	Body string `json:"body"`
}

// Location holds location data.
type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Name      string  `json:"name"`
	Address   string  `json:"address"`
}

// Media holds media message data (image, audio, video, document).
type Media struct {
	ID       string `json:"id"`
	MimeType string `json:"mime_type"`
	Caption  string `json:"caption"`
	Filename string `json:"filename"`
}

// VCard holds a shared contact.
type VCard struct {
	Name   VCardName    `json:"name"`
	Phones []VCardPhone `json:"phones"`
}

// VCardName holds contact name fields.
type VCardName struct {
	FormattedName string `json:"formatted_name"`
}

// VCardPhone holds a phone number from a shared contact.
type VCardPhone struct {
	Phone string `json:"phone"`
	Type  string `json:"type"`
}

// Status is a message delivery status update.
type Status struct {
	ID        string `json:"id"`
	Status    string `json:"status"` // "sent", "delivered", "read"
	Timestamp string `json:"timestamp"`
}
