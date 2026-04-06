package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/antimatter-studios/teamagentica/pkg/claudecli"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-claude/internal/usage"
)

// remoteProxyConn manages a persistent WebSocket connection to the workspace
// exec server. It's established on first use and reused for subsequent requests.
type remoteProxyConn struct {
	mu   sync.Mutex
	ws   *websocket.Conn
	url  string
	init bool
}

var remoteConn remoteProxyConn

// remoteUserMessage matches the protocol expected by the workspace exec server.
type remoteUserMessage struct {
	Type           string `json:"type"`
	Prompt         string `json:"prompt"`
	ConversationID string `json:"conversation_id"`
}

// remoteInitMessage configures the Claude session on the workspace side.
type remoteInitMessage struct {
	Model        string `json:"model"`
	SystemPrompt string `json:"system_prompt"`
	MCPConfig    string `json:"mcp_config"`
	MaxTurns     int    `json:"max_turns"`
}

// chatStreamRemote implements ChatStream for remote execution mode.
// It proxies the request over WebSocket to the workspace container's exec server.
func (h *Handler) chatStreamRemote(ctx context.Context, req pluginsdk.AgentChatRequest) <-chan pluginsdk.AgentStreamEvent {
	ch := make(chan pluginsdk.AgentStreamEvent, 32)

	go func() {
		defer close(ch)

		h.mu.RLock()
		model := h.model
		execWSURL := h.execWSURL
		mcpConfig := h.mcpConfig
		debug := h.debug
		h.mu.RUnlock()

		if req.Model != "" {
			model = req.Model
		}

		systemPrompt := req.SystemPrompt
		if systemPrompt == "" {
			systemPrompt = h.defaultPrompt
		}

		messages := convertMessages(req)
		prompt := lastUserMessage(messages)
		if prompt == "" {
			prompt = buildPromptWithSystem(messages, systemPrompt)
		}

		// Ensure WebSocket connection to workspace exec server.
		ws, err := h.ensureRemoteConn(execWSURL, model, systemPrompt, mcpConfig)
		if err != nil {
			ch <- pluginsdk.StreamError("remote connect: " + err.Error())
			return
		}

		// Send the user message.
		convID := req.SessionID
		if convID == "" {
			convID = req.AgentAlias
		}
		msg := remoteUserMessage{
			Type:           "message",
			Prompt:         prompt,
			ConversationID: convID,
		}
		data, _ := json.Marshal(msg)
		if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
			h.resetRemoteConn()
			ch <- pluginsdk.StreamError("remote write: " + err.Error())
			return
		}

		// Read stream events until we get a Done event.
		start := time.Now()
		var fullResponse string
		var respModel string
		var totalInput, totalOutput, cachedTokens int
		var costUSD float64
		var numTurns int
		var sessionID string

		for {
			_, rawMsg, err := ws.ReadMessage()
			if err != nil {
				h.resetRemoteConn()
				ch <- pluginsdk.StreamError("remote read: " + err.Error())
				return
			}

			var ev claudecli.StreamEvent
			if json.Unmarshal(rawMsg, &ev) != nil {
				continue
			}

			if ev.ErrMsg != "" {
				log.Printf("[remote] error: %s", ev.ErrMsg)
				ch <- pluginsdk.StreamError("remote: " + ev.ErrMsg)
				return
			}

			if ev.Text != "" {
				fullResponse += ev.Text
				ch <- pluginsdk.StreamToken(ev.Text)
			}
			if ev.ToolName != "" {
				ch <- pluginsdk.StreamToolCall(ev.ToolName, "")
			}
			if ev.ToolDone != "" {
				ch <- pluginsdk.StreamToolResult(ev.ToolDone, "")
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

			if ev.Done {
				break
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
			Backend:      "remote",
		})
		h.emitUsage("anthropic", respModel, totalInput, totalOutput, totalInput+totalOutput, cachedTokens, elapsed.Milliseconds(), req.UserID)

		if debug {
			h.emitEvent("chat_response", fmt.Sprintf("backend=remote model=%s tokens=%d+%d cost=$%.4f time=%dms response=%s",
				respModel, totalInput, totalOutput, costUSD, elapsed.Milliseconds(), truncateStr(fullResponse, 200)))
		} else {
			h.emitEvent("chat_response", fmt.Sprintf("backend=remote model=%s tokens=%d+%d cost=$%.4f time=%dms len=%d",
				respModel, totalInput, totalOutput, costUSD, elapsed.Milliseconds(), len(fullResponse)))
		}

		ch <- pluginsdk.AgentStreamEvent{
			Type: "done",
			Data: claudeDoneEvent{
				Response: fullResponse,
				Model:    respModel,
				Backend:  "remote",
				Usage: &pluginsdk.AgentUsage{
					PromptTokens:     totalInput,
					CompletionTokens: totalOutput,
					CachedTokens:     cachedTokens,
				},
				CostUSD:   costUSD,
				NumTurns:  numTurns,
				SessionID: sessionID,
			},
		}
	}()

	return ch
}

// ensureRemoteConn returns an active WebSocket connection, creating one if needed.
func (h *Handler) ensureRemoteConn(wsURL, model, systemPrompt, mcpConfig string) (*websocket.Conn, error) {
	remoteConn.mu.Lock()
	defer remoteConn.mu.Unlock()

	if remoteConn.ws != nil && remoteConn.init {
		return remoteConn.ws, nil
	}

	log.Printf("[remote] connecting to %s", wsURL)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", wsURL, err)
	}

	// Send init frame.
	init := remoteInitMessage{
		Model:        model,
		SystemPrompt: systemPrompt,
		MCPConfig:    mcpConfig,
	}
	if err := ws.WriteJSON(init); err != nil {
		ws.Close()
		return nil, fmt.Errorf("send init: %w", err)
	}

	// Wait for attached confirmation.
	var resp map[string]string
	if err := ws.ReadJSON(&resp); err != nil {
		ws.Close()
		return nil, fmt.Errorf("read attached: %w", err)
	}
	if resp["status"] != "attached" {
		ws.Close()
		return nil, fmt.Errorf("unexpected status: %v", resp)
	}

	remoteConn.ws = ws
	remoteConn.url = wsURL
	remoteConn.init = true

	log.Printf("[remote] connected to workspace exec server")
	return ws, nil
}

// resetRemoteConn closes and clears the remote connection so the next call reconnects.
func (h *Handler) resetRemoteConn() {
	remoteConn.mu.Lock()
	defer remoteConn.mu.Unlock()
	if remoteConn.ws != nil {
		remoteConn.ws.Close()
		remoteConn.ws = nil
		remoteConn.init = false
	}
}
