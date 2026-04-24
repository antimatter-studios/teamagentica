// Package exec provides a WebSocket-based exec server that proxies Codex CLI
// sessions into workspace containers. Mirrors plugins/agent-anthropic/internal/exec.
package exec

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/codexcli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/openai"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// InitMessage is sent by the sidecar after connecting. It configures the
// Codex CLI session (model, system prompt, etc).
type InitMessage struct {
	Model        string `json:"model"`
	SystemPrompt string `json:"system_prompt"`
	MCPConfig    string `json:"mcp_config"`
	MaxTurns     int    `json:"max_turns"`
}

// UserMessage is sent by the sidecar for each user turn.
type UserMessage struct {
	Type           string `json:"type"`
	Prompt         string `json:"prompt"`
	ConversationID string `json:"conversation_id"`
}

// Server exposes a codexcli.Client over WebSocket so an agent-openai
// sidecar can proxy Codex CLI execution into this workspace container.
type Server struct {
	client *codexcli.Client
}

// NewServer creates an exec server backed by the given codexcli.Client.
func NewServer(client *codexcli.Client) *Server {
	return &Server{client: client}
}

// Start listens on the given address (e.g. ":9100").
func (s *Server) Start(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/exec", s.handleExec)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	log.Printf("[exec-server] listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Printf("[exec-server] listen error: %v", err)
	}
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[exec] upgrade failed: %v", err)
		return
	}
	defer ws.Close()

	var init InitMessage
	if err := ws.ReadJSON(&init); err != nil {
		sendJSON(ws, map[string]string{"error": "invalid init frame: " + err.Error()})
		return
	}

	log.Printf("[exec] session started: model=%s", init.Model)
	sendJSON(ws, map[string]string{"status": "attached"})

	for {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			return
		}

		var um UserMessage
		if json.Unmarshal(msg, &um) != nil || um.Prompt == "" {
			continue
		}

		messages := []openai.Message{}
		if init.SystemPrompt != "" {
			messages = append(messages, openai.Message{Role: "system", Content: init.SystemPrompt})
		}
		messages = append(messages, openai.Message{Role: "user", Content: um.Prompt})

		ctx := r.Context()
		stream := s.client.ChatCompletionStream(ctx, init.Model, messages, nil, "", um.ConversationID)

		if err := streamEvents(ctx, ws, stream); err != nil {
			return
		}
	}
}

func streamEvents(ctx context.Context, ws *websocket.Conn, stream <-chan codexcli.StreamEvent) error {
	for ev := range stream {
		data, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		if werr := ws.WriteMessage(websocket.TextMessage, data); werr != nil {
			return werr
		}
	}
	return nil
}

func sendJSON(ws *websocket.Conn, v interface{}) {
	data, _ := json.Marshal(v)
	ws.WriteMessage(websocket.TextMessage, data)
}
