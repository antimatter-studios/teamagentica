// Standalone Claude exec server binary.
// Delivered to workspace containers via shared disk and started by the devbox entrypoint.
// Exposes a WebSocket endpoint on :9100/exec for agent-anthropic sidecar connections.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/antimatter-studios/teamagentica/pkg/claudecli"
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

	cliBinary := envOr("CLAUDE_CLI_BINARY", "/usr/local/bin/claude")
	workdir := envOr("CLAUDE_WORKDIR", "/workspace")
	claudeDir := envOr("CLAUDE_CONFIG_DIR", "/home/coder/.claude")
	debug := os.Getenv("PLUGIN_DEBUG") == "true"
	skipPerms := os.Getenv("CLAUDE_SKIP_PERMISSIONS") == "true"

	poolMax := 1
	if v := os.Getenv("CLAUDE_POOL_MAX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			poolMax = n
		}
	}

	addr := envOr("EXEC_SERVER_ADDR", ":9100")

	client := claudecli.NewClient(cliBinary, workdir, claudeDir, 600, debug)
	client.SetPoolMax(poolMax)
	if skipPerms {
		client.SetSkipPermissions(true)
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

func makeExecHandler(client *claudecli.Client) http.HandlerFunc {
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

			opts := &claudecli.ChatOptions{
				ConversationID: um.ConversationID,
			}

			ctx := r.Context()
			stream := client.ChatCompletionStream(
				ctx,
				init.Model,
				um.Prompt,
				init.SystemPrompt,
				init.MaxTurns,
				nil,
				init.MCPConfig,
				opts,
			)

			if err := streamEvents(ctx, ws, stream); err != nil {
				return
			}
		}
	}
}

func streamEvents(ctx context.Context, ws *websocket.Conn, stream <-chan claudecli.StreamEvent) error {
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
