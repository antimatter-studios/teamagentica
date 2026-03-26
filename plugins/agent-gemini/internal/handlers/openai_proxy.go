package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Fields that are OpenAI-specific and not supported by Google's Gemini API.
var unsupportedFields = []string{
	"store", "stream_options", "parallel_tool_calls",
	"logit_bias", "logprobs", "top_logprobs",
	"user", "service_tier",
}

// Google's OpenAI-compatible endpoint base URL.
const googleOpenAIBase = "https://generativelanguage.googleapis.com/v1beta/openai"

// OpenAIProxy reverse-proxies OpenAI-compatible requests to Google's Gemini API,
// injecting the plugin's API key. This allows other plugins (e.g. infra-agent-memory)
// to use Gemini models without needing their own API key.
func (h *Handler) OpenAIProxy(c *gin.Context) {
	h.mu.RLock()
	apiKey := h.apiKey
	debug := h.debug
	h.mu.RUnlock()

	if apiKey == "" {
		c.JSON(http.StatusBadGateway, gin.H{"error": "No Gemini API key configured"})
		return
	}

	// Build the upstream URL: /v1/chat/completions → /v1beta/openai/chat/completions
	// The route is registered as /v1/*path, so Gin gives us the sub-path.
	subPath := c.Param("path")
	if subPath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "missing path"})
		return
	}
	upstreamURL := googleOpenAIBase + subPath

	if debug {
		log.Printf("[openai-proxy] %s %s → %s", c.Request.Method, c.Request.URL.Path, upstreamURL)
	}

	// Read and sanitize the request body — strip OpenAI-specific fields
	// that Google's Gemini API doesn't support.
	var reqBody io.Reader = c.Request.Body
	if c.Request.Body != nil && c.Request.Method != http.MethodGet {
		raw, readErr := io.ReadAll(c.Request.Body)
		if readErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
			return
		}
		var payload map[string]interface{}
		if json.Unmarshal(raw, &payload) == nil {
			for _, field := range unsupportedFields {
				delete(payload, field)
			}
			if cleaned, marshalErr := json.Marshal(payload); marshalErr == nil {
				raw = cleaned
			}
		}
		reqBody = bytes.NewReader(raw)
	}

	// Create upstream request.
	upstreamReq, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, upstreamURL, reqBody)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create upstream request"})
		return
	}

	// Copy relevant headers from the original request.
	for _, header := range []string{"Content-Type", "Accept"} {
		if v := c.GetHeader(header); v != "" {
			upstreamReq.Header.Set(header, v)
		}
	}
	if upstreamReq.Header.Get("Content-Type") == "" {
		upstreamReq.Header.Set("Content-Type", "application/json")
	}

	// Inject the Gemini API key — Google's OpenAI-compat endpoint accepts Bearer auth.
	upstreamReq.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(upstreamReq)
	if err != nil {
		log.Printf("[openai-proxy] upstream error: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream request failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if debug {
		log.Printf("[openai-proxy] upstream response: %d", resp.StatusCode)
	}

	// Stream the response back to the caller with the same status and content type.
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}

	// For streaming responses, flush as we go.
	if strings.Contains(contentType, "text/event-stream") {
		c.Header("Content-Type", contentType)
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Status(resp.StatusCode)
		c.Writer.Flush()

		buf := make([]byte, 4096)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				c.Writer.Write(buf[:n])
				c.Writer.Flush()
			}
			if readErr != nil {
				break
			}
		}
		return
	}

	// Non-streaming: read full body and return.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to read upstream response"})
		return
	}

	c.Data(resp.StatusCode, contentType, body)
}
