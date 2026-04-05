package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/plugins/agent-claude/internal/anthropic"
	"github.com/antimatter-studios/teamagentica/plugins/agent-claude/internal/claudecli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-claude/internal/usage"
)

// ChatStream handles a streaming chat completion request.
// Writes SSE events as tokens arrive from the Claude CLI.
//
// SSE event types:
//   - token:       {"content": "..."}
//   - done:        {"response": "...", "model": "...", "backend": "...", "usage": {...}, ...}
//   - error:       {"error": "..."}
func (h *Handler) ChatStream(c *gin.Context) {
	userID := c.Request.Header.Get("X-Teamagentica-User-ID")

	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	messages := req.Conversation
	if len(messages) == 0 {
		if req.Message == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "message or conversation required"})
			return
		}
		messages = []anthropic.Message{
			{Role: "user", Content: req.Message},
		}
	}

	h.mu.RLock()
	model := h.model
	backend := h.backend
	mcpConfig := h.mcpConfig
	debug := h.debug
	h.mu.RUnlock()

	if req.Model != "" {
		model = req.Model
	}

	start := time.Now()

	// Set SSE headers.
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeaderNow()

	writeSSE := func(event string, data interface{}) {
		jsonData, err := json.Marshal(data)
		if err != nil {
			log.Printf("[stream] failed to marshal SSE data: %v", err)
			return
		}
		fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, jsonData)
		c.Writer.Flush()
	}

	writeError := func(msg string) {
		writeSSE("error", gin.H{"error": msg})
	}

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	switch backend {
	case "cli":
		if h.claudeCLI == nil {
			writeError("CLI backend not initialised")
			return
		}

		systemPrompt := req.SystemPrompt
		if systemPrompt == "" {
			systemPrompt = h.defaultPrompt
		}

		// For persistent CLI processes, send only the latest user message.
		// The system prompt is passed via --append-system-prompt at process
		// startup, and the session maintains conversation context.
		// Flattening the full history into one message breaks the CLI's
		// stream-json input parser.
		prompt := lastUserMessage(messages)
		if prompt == "" {
			prompt = buildPromptWithSystem(messages, systemPrompt)
		}

		var opts *claudecli.ChatOptions
		if req.WorkspaceID != "" || req.SessionID != "" {
			opts = &claudecli.ChatOptions{
				SessionID: req.SessionID,
			}
			if req.WorkspaceID != "" && isValidWorkspaceID(req.WorkspaceID) {
				opts.WorkspaceDir = h.workspaceDir + "/" + req.WorkspaceID
			}
		}

		stream := h.claudeCLI.ChatCompletionStream(ctx, model, prompt, req.SystemPrompt, req.MaxTurns, nil, mcpConfig, opts)

		var fullResponse string
		var respModel string
		var totalInput, totalOutput, cachedTokens int
		var costUSD float64
		var numTurns int
		var sessionID string

		for ev := range stream {
			if ev.Err != nil {
				log.Printf("[stream] Claude CLI error: %v", ev.Err)
				h.emitEvent("error", fmt.Sprintf("cli stream: %v", ev.Err))
				writeError("Claude stream error: " + ev.Err.Error())
				return
			}

			if ev.Text != "" {
				fullResponse += ev.Text
				writeSSE("token", gin.H{"content": ev.Text})
			}

			if ev.Model != "" {
				respModel = ev.Model
			}

			if ev.Usage != nil {
				totalInput = ev.Usage.InputTokens
				totalOutput = ev.Usage.OutputTokens
				cachedTokens = ev.Usage.CachedTokens
			}

			if ev.CostUSD > 0 {
				costUSD = ev.CostUSD
			}
			if ev.NumTurns > 0 {
				numTurns = ev.NumTurns
			}
			if ev.SessionID != "" {
				sessionID = ev.SessionID
			}
		}

		elapsed := time.Since(start)

		if respModel == "" {
			respModel = model
		}

		h.usage.RecordRequest(usage.RequestRecord{
			Model:        respModel,
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
			TotalTokens:  totalInput + totalOutput,
			CachedTokens: cachedTokens,
			DurationMs:   elapsed.Milliseconds(),
			Backend:      "cli",
		})
		h.emitUsage("anthropic", respModel, totalInput, totalOutput, totalInput+totalOutput, cachedTokens, elapsed.Milliseconds(), userID)

		if debug {
			h.emitEvent("chat_response", fmt.Sprintf("backend=cli model=%s tokens=%d+%d cost=$%.4f time=%dms response=%s",
				respModel, totalInput, totalOutput, costUSD, elapsed.Milliseconds(), truncateStr(fullResponse, 200)))
		} else {
			h.emitEvent("chat_response", fmt.Sprintf("backend=cli model=%s tokens=%d+%d cost=$%.4f time=%dms len=%d",
				respModel, totalInput, totalOutput, costUSD, elapsed.Milliseconds(), len(fullResponse)))
		}

		writeSSE("done", gin.H{
			"response": fullResponse,
			"model":    respModel,
			"backend":  "cli",
			"usage": gin.H{
				"prompt_tokens":     totalInput,
				"completion_tokens": totalOutput,
				"cached_tokens":     cachedTokens,
			},
			"cost_usd":   costUSD,
			"num_turns":  numTurns,
			"session_id": sessionID,
		})

	case "api_key":
		// API key backend — no streaming support yet, return error.
		writeError("streaming not yet supported for api_key backend on agent-claude")

	default:
		writeError(fmt.Sprintf("unknown backend %q", backend))
	}
}
