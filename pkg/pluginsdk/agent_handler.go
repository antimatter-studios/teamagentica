package pluginsdk

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// RegisterAgentChat registers POST /chat on the given router, wiring an
// AgentProvider's streaming output to SSE. The SDK owns the SSE framing,
// headers, request parsing, and error handling — the provider just emits events.
func RegisterAgentChat(router gin.IRouter, provider AgentProvider) {
	router.POST("/chat", agentChatHandler(provider))
}

func agentChatHandler(provider AgentProvider) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req AgentChatRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}

		if req.Message == "" && len(req.Conversation) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "message or conversation required"})
			return
		}

		// Populate UserID from header (set by relay/kernel).
		req.UserID = c.Request.Header.Get("X-Teamagentica-User-ID")

		// Set SSE headers.
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("X-Accel-Buffering", "no")
		c.Writer.WriteHeaderNow()

		ctx := c.Request.Context()
		stream := provider.ChatStream(ctx, req)

		for ev := range stream {
			writeAgentSSE(c.Writer, ev)
		}
	}
}

// writeAgentSSE writes a single AgentStreamEvent as an SSE event.
func writeAgentSSE(w gin.ResponseWriter, ev AgentStreamEvent) {
	data, err := json.Marshal(ev.Data)
	if err != nil {
		log.Printf("[agent-sse] failed to marshal event %s: %v", ev.Type, err)
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
	w.Flush()
}

// Helper constructors for AgentStreamEvent — reduces boilerplate in providers.

// StreamToken creates a token event.
func StreamToken(content string) AgentStreamEvent {
	return AgentStreamEvent{Type: "token", Data: TokenEvent{Content: content}}
}

// StreamToolCall creates a tool_call event.
func StreamToolCall(name, arguments string) AgentStreamEvent {
	return AgentStreamEvent{Type: "tool_call", Data: ToolCallEvent{Name: name, Arguments: arguments}}
}

// StreamToolResult creates a tool_result event.
func StreamToolResult(name, result string) AgentStreamEvent {
	return AgentStreamEvent{Type: "tool_result", Data: ToolResultEvent{Name: name, Result: result}}
}

// StreamToolError creates a tool_result event with an error.
func StreamToolError(name, errMsg string) AgentStreamEvent {
	return AgentStreamEvent{Type: "tool_result", Data: ToolResultEvent{Name: name, Error: errMsg}}
}

// StreamDone creates a done event with the final response.
func StreamDone(done DoneEvent) AgentStreamEvent {
	return AgentStreamEvent{Type: "done", Data: done}
}

// StreamError creates an error event.
func StreamError(errMsg string) AgentStreamEvent {
	return AgentStreamEvent{Type: "error", Data: ErrorEvent{Error: errMsg}}
}
