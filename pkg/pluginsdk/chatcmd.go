package pluginsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// ── Chat command spec types (declared by plugins in their schema) ────────────

// ChatCommandParam describes a single parameter for a chat command.
type ChatCommandParam struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`              // "string", "integer", "boolean"
	Required    bool   `json:"required,omitempty"`
}

// ChatCommand describes a slash command a plugin exposes to human users via
// any messaging transport (Discord, Telegram, web chat, etc.).
// Commands are grouped by Namespace (e.g. "workspace", "alias") and routed
// by infra-chat-command-server.
type ChatCommand struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Namespace   string             `json:"namespace,omitempty"` // grouping prefix, e.g. "workspace"
	Endpoint    string             `json:"endpoint"`            // POST endpoint on this plugin
	Params      []ChatCommandParam `json:"params,omitempty"`
	Platforms   []string           `json:"platforms,omitempty"` // restrict to specific transports; empty = all
}

// ── Chat command response types (returned by command handlers) ───────────────

// ChatCommandResponse is returned by a plugin's chat command endpoint.
// Type determines which content field is used.
type ChatCommandResponse struct {
	Type    string        `json:"type"`              // "text", "table", "embed", "error"
	Text    string        `json:"text,omitempty"`    // for type "text" or "error"
	Table   *TableContent `json:"table,omitempty"`   // for type "table"
	Embeds  []EmbedContent `json:"embeds,omitempty"` // for type "embed"
}

// TableContent describes tabular data that transports can render as code blocks,
// Discord embeds, or Telegram pre-formatted text.
type TableContent struct {
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
}

// EmbedContent is a rich card with a title, optional color, and key-value fields.
// Transports render this as Discord embeds, Telegram formatted messages, etc.
type EmbedContent struct {
	Title  string       `json:"title,omitempty"`
	Color  int          `json:"color,omitempty"` // RGB integer
	Fields []EmbedField `json:"fields,omitempty"`
}

// EmbedField is a single key-value pair within an EmbedContent.
type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// ── Response constructors ────────────────────────────────────────────────────

// TextResponse creates a ChatCommandResponse with plain text content.
func TextResponse(text string) ChatCommandResponse {
	return ChatCommandResponse{Type: "text", Text: text}
}

// ErrorResponse creates a ChatCommandResponse indicating an error.
func ErrorResponse(msg string) ChatCommandResponse {
	return ChatCommandResponse{Type: "error", Text: msg}
}

// TableResponse creates a ChatCommandResponse with tabular data.
func TableResponse(headers []string, rows [][]string) ChatCommandResponse {
	return ChatCommandResponse{Type: "table", Table: &TableContent{Headers: headers, Rows: rows}}
}

// EmbedResponse creates a ChatCommandResponse with one or more rich embeds.
func EmbedResponse(embeds ...EmbedContent) ChatCommandResponse {
	return ChatCommandResponse{Type: "embed", Embeds: embeds}
}

// ── Push registration (mirrors RegisterToolsWithMCP) ─────────────────────────

// RegisterChatCommands pushes this plugin's chat command definitions to the
// chat command server. Call from an OnPluginAvailable("chat:commands", ...) callback.
func (c *Client) RegisterChatCommands(serverPluginID string, commands []ChatCommand) error {
	payload := map[string]interface{}{
		"plugin_id": c.registration.ID,
		"commands":  commands,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal commands: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = c.RouteToPlugin(ctx, serverPluginID, "POST", "/commands/register", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("register commands with %s: %w", serverPluginID, err)
	}
	log.Printf("pluginsdk: registered %d chat commands with %s", len(commands), serverPluginID)
	return nil
}
