package handlers

import (
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// OpenAIProxy reverse-proxies OpenAI-compatible requests to the internal Ollama server.
// This allows other plugins (e.g. infra-agent-memory-gateway) to use Ollama models
// via standard OpenAI endpoints without knowing the internal Ollama address.
func (h *Handler) OpenAIProxy(c *gin.Context) {
	h.mu.RLock()
	endpoint := h.endpoint
	debug := h.debug
	h.mu.RUnlock()

	// Build the upstream URL: /v1/chat/completions → http://localhost:11434/v1/chat/completions
	subPath := c.Param("path")
	if subPath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "missing path"})
		return
	}
	upstreamURL := endpoint + "/v1" + subPath

	if debug {
		log.Printf("[openai-proxy] %s %s → %s", c.Request.Method, c.Request.URL.Path, upstreamURL)
	}

	upstreamReq, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, upstreamURL, c.Request.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create upstream request"})
		return
	}

	// Copy relevant headers.
	for _, header := range []string{"Content-Type", "Accept"} {
		if v := c.GetHeader(header); v != "" {
			upstreamReq.Header.Set(header, v)
		}
	}
	if upstreamReq.Header.Get("Content-Type") == "" {
		upstreamReq.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(upstreamReq)
	if err != nil {
		log.Printf("[openai-proxy] upstream error: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "ollama request failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if debug {
		log.Printf("[openai-proxy] upstream response: %d", resp.StatusCode)
	}

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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to read upstream response"})
		return
	}

	c.Data(resp.StatusCode, contentType, body)
}
