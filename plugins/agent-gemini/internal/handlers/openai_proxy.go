package handlers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-gemini/internal/usage"
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
// injecting the plugin's API key. This allows other plugins (e.g. infra-agent-memory-gateway)
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

	userID := c.Request.Header.Get("X-Teamagentica-User-ID")

	// Read and sanitize the request body — strip OpenAI-specific fields
	// that Google's Gemini API doesn't support.
	var reqBody io.Reader = c.Request.Body
	var requestedModel string
	if c.Request.Body != nil && c.Request.Method != http.MethodGet {
		raw, readErr := io.ReadAll(c.Request.Body)
		if readErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
			return
		}
		var payload map[string]interface{}
		if json.Unmarshal(raw, &payload) == nil {
			if m, ok := payload["model"].(string); ok {
				requestedModel = m
			}
			for _, field := range unsupportedFields {
				delete(payload, field)
			}
			if cleaned, marshalErr := json.Marshal(payload); marshalErr == nil {
				raw = cleaned
			}
		}
		reqBody = bytes.NewReader(raw)
	}

	endpointType := classifyEndpoint(subPath)

	if debug {
		log.Printf("[openai-proxy] %s %s → %s (model=%s type=%s)",
			c.Request.Method, c.Request.URL.Path, upstreamURL, requestedModel, endpointType)
	}

	start := time.Now()

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

	// Capture rate limit headers from Google.
	h.usage.UpdateRateLimit(resp.Header)

	elapsed := time.Since(start)

	// Stream the response back to the caller with the same status and content type.
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}

	// For streaming responses, parse SSE to capture usage from the final chunk.
	if strings.Contains(contentType, "text/event-stream") {
		h.proxyStream(c, resp, subPath, requestedModel, userID, endpointType, start)
		return
	}

	// Non-streaming: read full body, extract usage, and return.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to read upstream response"})
		return
	}

	// Extract usage from successful responses and report it.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		h.reportProxyUsage(subPath, body, requestedModel, userID, endpointType, elapsed)
	}

	if debug || resp.StatusCode >= 400 {
		log.Printf("[openai-proxy] %s %s status=%d time=%dms model=%s",
			c.Request.Method, subPath, resp.StatusCode, elapsed.Milliseconds(), requestedModel)
	}

	c.Data(resp.StatusCode, contentType, body)
}

// proxyStream handles SSE streaming responses, forwarding chunks to the client
// while parsing the final chunk's usage data for cost tracking.
func (h *Handler) proxyStream(c *gin.Context, resp *http.Response, subPath, requestedModel, userID, endpointType string, start time.Time) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(resp.StatusCode)
	c.Writer.Flush()

	var lastUsage *openaiUsageBlock
	var responseModel string

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// Forward every line to the client immediately.
		c.Writer.Write([]byte(line + "\n"))
		c.Writer.Flush()

		// Parse SSE data lines for usage info.
		if strings.HasPrefix(line, "data: ") {
			data := line[6:]
			if data == "[DONE]" {
				continue
			}
			var chunk streamChunk
			if json.Unmarshal([]byte(data), &chunk) == nil {
				if chunk.Model != "" {
					responseModel = chunk.Model
				}
				if chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 {
					lastUsage = &chunk.Usage
				}
			}
		}
	}

	elapsed := time.Since(start)

	// Report usage from the final chunk if present.
	if lastUsage != nil {
		model := responseModel
		if model == "" {
			model = requestedModel
		}
		h.reportUsageFromBlock(model, lastUsage, userID, endpointType, elapsed)
	}

	h.mu.RLock()
	debug := h.debug
	h.mu.RUnlock()
	if debug {
		tokens := 0
		if lastUsage != nil {
			tokens = lastUsage.TotalTokens
		}
		log.Printf("[openai-proxy] stream complete: %s model=%s tokens=%d time=%dms",
			subPath, responseModel, tokens, elapsed.Milliseconds())
	}
}

// streamChunk represents a single SSE chunk from an OpenAI-compatible streaming response.
type streamChunk struct {
	Model string          `json:"model"`
	Usage openaiUsageBlock `json:"usage"`
}

// openaiUsageBlock captures the usage block from OpenAI-compatible responses.
type openaiUsageBlock struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// openaiUsageResponse captures model + usage from a non-streaming response.
type openaiUsageResponse struct {
	Model string           `json:"model"`
	Usage openaiUsageBlock `json:"usage"`
}

// reportProxyUsage extracts token counts from an OpenAI-compatible response body and reports usage.
func (h *Handler) reportProxyUsage(path string, body []byte, requestedModel, userID, endpointType string, elapsed time.Duration) {
	var parsed openaiUsageResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return
	}
	if parsed.Usage.TotalTokens == 0 && parsed.Usage.PromptTokens == 0 {
		return
	}

	model := parsed.Model
	if model == "" {
		model = requestedModel
	}

	h.reportUsageFromBlock(model, &parsed.Usage, userID, endpointType, elapsed)
}

// reportUsageFromBlock reports usage to the cost tracking system and the local tracker.
func (h *Handler) reportUsageFromBlock(model string, u *openaiUsageBlock, userID, endpointType string, elapsed time.Duration) {
	if model == "" {
		h.mu.RLock()
		model = h.model
		h.mu.RUnlock()
	}

	// Report to cost tracking via SDK event.
	if h.sdk != nil {
		h.sdk.ReportUsage(pluginsdk.UsageReport{
			UserID:       userID,
			Provider:     "gemini",
			Model:        model,
			RecordType:   endpointType,
			InputTokens:  u.PromptTokens,
			OutputTokens: u.CompletionTokens,
			TotalTokens:  u.TotalTokens,
			DurationMs:   elapsed.Milliseconds(),
		})
	}

	// Record in local tracker so /usage and /usage/records reflect proxy traffic.
	h.usage.RecordRequest(usage.RequestRecord{
		Model:        model,
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
		DurationMs:   elapsed.Milliseconds(),
	})

	h.mu.RLock()
	debug := h.debug
	h.mu.RUnlock()
	if debug {
		log.Printf("[openai-proxy] usage: model=%s type=%s tokens=%d+%d time=%dms",
			model, endpointType, u.PromptTokens, u.CompletionTokens, elapsed.Milliseconds())
	}
}

// classifyEndpoint determines the endpoint type from the request path.
func classifyEndpoint(path string) string {
	switch {
	case strings.Contains(path, "embeddings"):
		return "embedding"
	case strings.Contains(path, "chat/completions"):
		return "token"
	case strings.Contains(path, "images"):
		return "request"
	default:
		return "token"
	}
}
