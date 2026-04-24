// Standalone Codex exec server binary.
// Delivered to workspace containers via shared disk and started by the devbox entrypoint.
// Exposes a WebSocket endpoint on :9100/exec for agent-openai sidecar connections.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/codexcli"
	"github.com/antimatter-studios/teamagentica/plugins/agent-openai/internal/openai"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type initMessage struct {
	Model        string `json:"model"`
	SystemPrompt string `json:"system_prompt"`
	MCPConfig    string `json:"mcp_config"`
	MaxTurns     int    `json:"max_turns"`
}

type userMessage struct {
	Type           string `json:"type"`
	Prompt         string `json:"prompt"`
	ConversationID string `json:"conversation_id"`
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cliBinary := envOr("CODEX_CLI_BINARY", "/usr/local/bin/codex")
	workdir := envOr("CODEX_WORKDIR", "/workspace")
	codexHome := envOr("CODEX_HOME", "/home/coder/.codex")
	debug := os.Getenv("PLUGIN_DEBUG") == "true"

	addr := envOr("EXEC_SERVER_ADDR", ":9100")

	client := codexcli.NewClient(cliBinary, workdir, codexHome, 600, debug)
	if err := client.StartAppServer(); err != nil {
		log.Fatalf("[exec-server] failed to start codex app-server: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/exec", makeExecHandler(client))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	log.Printf("[exec-server] listening on %s (binary=%s workdir=%s)", addr, cliBinary, workdir)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("[exec-server] listen error: %v", err)
	}
}

func makeExecHandler(client *codexcli.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("[exec] upgrade failed: %v", err)
			return
		}
		defer ws.Close()

		var init initMessage
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

			var um userMessage
			if json.Unmarshal(msg, &um) != nil || um.Prompt == "" {
				continue
			}

			messages := []openai.Message{}
			if init.SystemPrompt != "" {
				messages = append(messages, openai.Message{Role: "system", Content: init.SystemPrompt})
			}
			messages = append(messages, openai.Message{Role: "user", Content: um.Prompt})

			ctx := r.Context()
			stream := client.ChatCompletionStream(ctx, init.Model, messages, nil, "", um.ConversationID)

			if err := streamEvents(ctx, ws, stream); err != nil {
				return
			}
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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
