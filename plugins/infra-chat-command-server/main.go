package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-chat-command-server/internal/registry"
)

const defaultPort = 8081

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()
	hostname, _ := os.Hostname()
	manifest := pluginsdk.LoadManifest()

	reg := registry.New()

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		SchemaFunc: func() map[string]interface{} {
			return map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sdkClient.Start(ctx)

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("chat-cmd-server: failed to fetch config: %v", err)
	}
	debug := pluginConfig["PLUGIN_DEBUG"] == "true"

	router := gin.Default()

	// Health check.
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "plugin": manifest.ID})
	})

	// List all registered commands.
	router.GET("/commands", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"commands": reg.All()})
	})

	// Push-based command registration from plugins.
	router.POST("/commands/register", func(c *gin.Context) {
		var req struct {
			PluginID string                    `json:"plugin_id" binding:"required"`
			Commands []pluginsdk.ChatCommand   `json:"commands" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		seq := reg.Register(req.PluginID, req.Commands)
		log.Printf("chat-cmd-server: %s registered %d commands (seq=%d)", req.PluginID, len(req.Commands), seq)

		// Broadcast update event so transports can refresh their command lists.
		broadcastUpdate(sdkClient, reg)

		c.JSON(http.StatusOK, gin.H{"registered": len(req.Commands)})
	})

	// Invoke a command by name.
	router.POST("/invoke", func(c *gin.Context) {
		var req struct {
			Command  string            `json:"command" binding:"required"`
			Params   map[string]string `json:"params"`
			Platform string            `json:"platform"` // "discord", "telegram", etc.
			UserID   string            `json:"user_id"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		entry, ok := reg.Lookup(req.Command)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "unknown command: " + req.Command})
			return
		}

		// Check platform restriction.
		if len(entry.Command.Platforms) > 0 && req.Platform != "" {
			allowed := false
			for _, p := range entry.Command.Platforms {
				if p == req.Platform {
					allowed = true
					break
				}
			}
			if !allowed {
				c.JSON(http.StatusForbidden, gin.H{"error": "command not available on " + req.Platform})
				return
			}
		}

		if debug {
			log.Printf("chat-cmd-server: invoking %s on %s endpoint=%s", req.Command, entry.PluginID, entry.Command.Endpoint)
		}

		// Route to the owning plugin's command endpoint.
		payload, _ := json.Marshal(map[string]interface{}{
			"params":   req.Params,
			"platform": req.Platform,
			"user_id":  req.UserID,
		})

		respBody, err := sdkClient.RouteToPlugin(
			c.Request.Context(),
			entry.PluginID,
			"POST",
			entry.Command.Endpoint,
			bytes.NewReader(payload),
		)
		if err != nil {
			log.Printf("chat-cmd-server: invoke failed: %v", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "command execution failed: " + err.Error()})
			return
		}

		// Pass through the plugin's ChatCommandResponse.
		c.Data(http.StatusOK, "application/json", respBody)
	})

	// Discovery: used by transports to build initial command list on startup.
	sdkClient.Events().On("plugin:registered", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		discoverCommandsFromPlugin(sdkClient, reg, event)
	}))

	// Auto-deregister when plugins stop.
	sdkClient.Events().On("plugin:stopped", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var payload struct {
			Plugin string `json:"plugin_id"`
		}
		if json.Unmarshal([]byte(event.Detail), &payload) == nil && payload.Plugin != "" {
			if _, removed := reg.Deregister(payload.Plugin); removed {
				log.Printf("chat-cmd-server: deregistered commands for stopped plugin %s", payload.Plugin)
				broadcastUpdate(sdkClient, reg)
			}
		}
	}))

	sdkClient.ListenAndServe(defaultPort, router)
}

// broadcastUpdate emits a chat:commands:updated event with the full command list.
func broadcastUpdate(sdk *pluginsdk.Client, reg *registry.Registry) {
	entries := reg.All()

	// Build a summary for the event detail.
	cmds := make([]map[string]string, 0, len(entries))
	for _, e := range entries {
		name := e.Command.Name
		if e.Command.Namespace != "" {
			name = e.Command.Namespace + ":" + e.Command.Name
		}
		cmds = append(cmds, map[string]string{
			"name":        name,
			"description": e.Command.Description,
			"plugin_id":   e.PluginID,
		})
	}

	detail, _ := json.Marshal(map[string]interface{}{
		"commands": cmds,
		"count":    len(cmds),
	})
	sdk.PublishEvent("chat:commands:updated", string(detail))
}

// discoverCommandsFromPlugin checks if a newly registered plugin has chat:commands
// capability and fetches its chat_commands from the schema.
func discoverCommandsFromPlugin(sdk *pluginsdk.Client, reg *registry.Registry, event pluginsdk.EventCallback) {
	plugins, err := sdk.SearchPlugins("chat:commands")
	if err != nil {
		return
	}

	for _, p := range plugins {
		schema, err := sdk.GetPluginSchema(p.ID)
		if err != nil {
			log.Printf("chat-cmd-server: failed to fetch schema from %s: %v", p.ID, err)
			continue
		}

		cmdsRaw, ok := schema["chat_commands"]
		if !ok {
			continue
		}

		data, err := json.Marshal(cmdsRaw)
		if err != nil {
			continue
		}

		var commands []pluginsdk.ChatCommand
		if err := json.Unmarshal(data, &commands); err != nil {
			log.Printf("chat-cmd-server: bad chat_commands from %s: %v", p.ID, err)
			continue
		}

		if len(commands) > 0 {
			seq := reg.Register(p.ID, commands)
			log.Printf("chat-cmd-server: discovered %d commands from %s (seq=%d)", len(commands), p.ID, seq)
		}
	}

	broadcastUpdate(sdk, reg)
}
