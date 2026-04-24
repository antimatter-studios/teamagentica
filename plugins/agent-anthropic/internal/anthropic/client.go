package anthropic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// ToolDef describes a tool available for function calling.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Message represents a single chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ImageURLs is populated on user turns when the client attached images.
	// API backends convert these to image content blocks; CLI backends download
	// them locally and @-mention the resulting paths.
	ImageURLs []string `json:"-"`
	// ToolUseBlocks is set when the assistant response includes tool calls.
	ToolUseBlocks []ContentBlock `json:"-"`
	// ToolResults is set when sending tool results back.
	ToolResults []ToolResult `json:"-"`
}

// ImageSource describes the source of an image block sent to the Anthropic
// Messages API. Only the URL variant is currently used.
type ImageSource struct {
	Type string `json:"type"` // "url" | "base64"
	URL  string `json:"url,omitempty"`
	// MediaType + Data are only set when Type == "base64".
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
}

// RequestBlock is a content block sent to the Anthropic Messages API as part
// of a user message's content array. The Anthropic API supports text + image
// blocks in a single user turn.
type RequestBlock struct {
	Type   string       `json:"type"` // "text" | "image"
	Text   string       `json:"text,omitempty"`
	Source *ImageSource `json:"source,omitempty"`
}

// BuildUserContent returns the API-ready content payload for a user message.
// When no images are attached it returns the plain content string so the
// request body stays backward-compatible with the simpler string form.
// When ImageURLs is non-empty it emits an array of blocks: one text block
// followed by one image block per URL.
func BuildUserContent(m Message) interface{} {
	if len(m.ImageURLs) == 0 {
		return m.Content
	}
	blocks := make([]RequestBlock, 0, len(m.ImageURLs)+1)
	blocks = append(blocks, RequestBlock{Type: "text", Text: m.Content})
	for _, u := range m.ImageURLs {
		blocks = append(blocks, RequestBlock{
			Type:   "image",
			Source: &ImageSource{Type: "url", URL: u},
		})
	}
	return blocks
}

// DownloadImage fetches a URL to a temp file on disk and returns the path.
// The caller is responsible for removing the file once it is no longer needed.
// This mirrors the helper used by agent-openai for the Codex CLI backend.
func DownloadImage(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("downloading image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("image download returned status %d", resp.StatusCode)
	}

	ext := filepath.Ext(url)
	if ext == "" || len(ext) > 5 {
		ext = ".jpg"
	}

	f, err := os.CreateTemp("", "teamagentica-anthropic-img-*"+ext)
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("writing image: %w", err)
	}

	return f.Name(), nil
}

// ToolResult is a tool result content block sent as part of a user message.
type ToolResult struct {
	Type      string `json:"type"`        // "tool_result"
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

// Usage tracks token usage for a request.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CacheRead    int `json:"cache_read_input_tokens"`
	CacheCreate  int `json:"cache_creation_input_tokens"`
}

// ContentBlock is a block within the response.
type ContentBlock struct {
	Type  string                 `json:"type"`
	Text  string                 `json:"text,omitempty"`
	ID    string                 `json:"id,omitempty"`    // for tool_use
	Name  string                 `json:"name,omitempty"`  // for tool_use
	Input map[string]interface{} `json:"input,omitempty"` // for tool_use
}

// ChatResponse is the response from the Anthropic messages API.
type ChatResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []ContentBlock `json:"content"`
	Model      string         `json:"model"`
	StopReason string         `json:"stop_reason"`
	Usage      Usage          `json:"usage"`
}

