package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
)

type aliasTarget struct {
	Plugin string
	Model  string
}

type llmProxy struct {
	mu     sync.RWMutex
	target aliasTarget
}

func (p *llmProxy) setTarget(t aliasTarget) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.target = t
}

func (p *llmProxy) getTarget() aliasTarget {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.target
}

const lcmServerURL = "http://localhost:8092"

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()
	hostname, _ := os.Hostname()
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8091

	proxy := &llmProxy{}

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		SchemaFunc: func() map[string]interface{} {
			schema := map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
			if stats := fetchLCMStats(); stats != nil {
				schema["lcm_stats"] = stats
			}
			return schema
		},
		ToolsFunc: func() interface{} {
			return toolDefs()
		},
	})

	ctx := context.Background()
	sdkClient.Start(ctx)

	// Resolve LLM alias from config.
	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Printf("WARNING: failed to fetch config: %v", err)
	} else {
		applyConfig(sdkClient, proxy, pluginConfig)
	}

	// Hot-reload config.
	events.OnConfigUpdate(sdkClient, func(p events.ConfigUpdatePayload) {
		applyConfig(sdkClient, proxy, p.Config)
	})

	router := gin.Default()

	// Health — also pings LCM Node.js server.
	router.GET("/health", func(c *gin.Context) {
		resp, err := http.Get(lcmServerURL + "/health")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"status": "degraded", "sidecar": "healthy", "lcm": "unreachable"})
			return
		}
		defer resp.Body.Close()
		var lcmHealth map[string]interface{}
		body, _ := io.ReadAll(resp.Body)
		json.Unmarshal(body, &lcmHealth)
		c.JSON(http.StatusOK, gin.H{"status": "healthy", "sidecar": "healthy", "lcm": lcmHealth})
	})

	// MCP tool discovery.
	router.GET("/mcp", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"tools": toolDefs()})
	})

	// Dynamic config options.
	router.GET("/config/options/:field", configOptionsHandler(sdkClient))

	// MCP tool endpoints — proxy to LCM Node.js server.
	router.POST("/mcp/store_messages", proxyToLCM("/ingest"))
	router.POST("/mcp/get_context", proxyToLCM("/assemble"))
	router.POST("/mcp/search_messages", proxyToLCM("/grep"))
	router.POST("/mcp/expand_summary", proxyToLCM("/expand"))

	// Episodic browsing — proxy GET requests to Node.js LCM server.
	router.GET("/conversations", proxyGetToLCM("/conversations"))
	router.GET("/conversations/:id/messages", func(c *gin.Context) {
		id := c.Param("id")
		query := ""
		if c.Request.URL.RawQuery != "" {
			query = "?" + c.Request.URL.RawQuery
		}
		proxyGetToLCM("/conversations/" + id + "/messages" + query)(c)
	})

	// Internal LLM proxy — Node.js calls this for summarization.
	router.POST("/internal/llm/complete", llmCompleteHandler(sdkClient, proxy))

	// Push tools to MCP server when it becomes available.
	sdkClient.OnPluginAvailable("infra:mcp-server", func(p pluginsdk.PluginInfo) {
		if err := sdkClient.RegisterToolsWithMCP(p.ID, toolDefs()); err != nil {
			log.Printf("failed to register tools with MCP: %v", err)
		}
	})

	sdkClient.ListenAndServe(defaultPort, router)
}

// proxyToLCM forwards a request to the LCM Node.js server.
func proxyToLCM(path string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, "POST", lcmServerURL+path, c.Request.Body)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("[lcm] proxy to %s failed: %v", path, err)
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("LCM server unreachable: %v", err)})
			return
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		c.Data(resp.StatusCode, "application/json", body)
	}
}

// proxyGetToLCM forwards a GET request to the LCM Node.js server.
func proxyGetToLCM(path string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, "GET", lcmServerURL+path, nil)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("[lcm] GET proxy to %s failed: %v", path, err)
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("LCM server unreachable: %v", err)})
			return
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		c.Data(resp.StatusCode, "application/json", body)
	}
}

// llmCompleteHandler forwards LLM requests from the Node.js server to the configured LLM plugin.
func llmCompleteHandler(sdk *pluginsdk.Client, proxy *llmProxy) gin.HandlerFunc {
	return func(c *gin.Context) {
		target := proxy.getTarget()
		if target.Plugin == "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no LLM alias configured — set LCM_LLM_ALIAS"})
			return
		}

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
		defer cancel()

		// Inject the resolved model into the request.
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		req["model"] = target.Model
		reqBody, _ := json.Marshal(req)

		respBody, err := sdk.RouteToPlugin(ctx, target.Plugin, "POST", "/v1/chat/completions",
			io.NopCloser(bytes.NewReader(reqBody)))
		if err != nil {
			log.Printf("[llm] RouteToPlugin(%s) failed: %v", target.Plugin, err)
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}

		c.Data(http.StatusOK, "application/json", respBody)
	}
}

// applyConfig resolves the LLM alias for summarization.
func applyConfig(sdk *pluginsdk.Client, proxy *llmProxy, config map[string]string) {
	alias := config["LCM_LLM_ALIAS"]
	if alias == "" {
		return
	}

	resolved, err := resolveAlias(sdk, alias)
	if err != nil {
		log.Printf("[config] failed to resolve LLM alias %q: %v", alias, err)
		return
	}

	proxy.setTarget(resolved)
	log.Printf("[config] LLM: %s/%s (alias=%s)", resolved.Plugin, resolved.Model, alias)
}

// resolveAlias fetches an alias from the alias-registry.
func resolveAlias(sdk *pluginsdk.Client, aliasName string) (aliasTarget, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	body, err := sdk.RouteToPlugin(ctx, "infra-agent-registry", "GET", "/alias/"+aliasName, nil)
	if err != nil {
		return aliasTarget{}, fmt.Errorf("fetch alias %q: %w", aliasName, err)
	}

	var resp struct {
		Plugin string `json:"plugin"`
		Model  string `json:"model"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return aliasTarget{}, fmt.Errorf("parse alias %q: %w", aliasName, err)
	}

	return aliasTarget{Plugin: resp.Plugin, Model: resp.Model}, nil
}

// configOptionsHandler serves dynamic dropdown options for alias selection.
func configOptionsHandler(sdk *pluginsdk.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		field := c.Param("field")
		if field != "LCM_LLM_ALIAS" {
			c.JSON(http.StatusOK, gin.H{"options": []string{}})
			return
		}

		// Show agent:chat aliases + personas that resolve to agent:chat plugins.
		options := fetchLLMOptions(sdk)
		c.JSON(http.StatusOK, gin.H{"options": options})
	}
}

// fetchLLMOptions returns agent:chat aliases plus personas that resolve to agent:chat plugins.
func fetchLLMOptions(sdk *pluginsdk.Client) []string {
	// Get agent:chat plugins.
	plugins, err := sdk.SearchPlugins("agent:chat")
	if err != nil {
		log.Printf("[config-options] failed to search agent:chat plugins: %v", err)
		return []string{}
	}
	chatPlugins := make(map[string]bool, len(plugins))
	for _, p := range plugins {
		chatPlugins[p.ID] = true
	}

	// Get all aliases, filter to agent:chat.
	aliases, err := sdk.FetchAliases()
	if err != nil {
		log.Printf("[config-options] failed to fetch aliases: %v", err)
		return []string{}
	}

	var chatAliases []string
	chatSet := make(map[string]bool)
	seen := make(map[string]bool)
	for _, a := range aliases {
		pluginID := a.Target
		for i, ch := range a.Target {
			if ch == ':' {
				pluginID = a.Target[:i]
				break
			}
		}
		if chatPlugins[pluginID] {
			chatAliases = append(chatAliases, a.Name)
			chatSet[strings.ToLower(a.Name)] = true
			seen[strings.ToLower(a.Name)] = true
		}
	}

	// After the persona/alias merge, chatAliases already includes all personas
	// with agent:chat capability. No separate persona lookup needed.
	return chatAliases
}

// fetchLCMStats pings the LCM server for schema display.
func fetchLCMStats() map[string]interface{} {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", lcmServerURL+"/health", nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return map[string]interface{}{"status": "lcm server not running"}
	}
	defer resp.Body.Close()

	var stats map[string]interface{}
	body, _ := io.ReadAll(resp.Body)
	json.Unmarshal(body, &stats)
	return stats
}

// toolDefs returns the MCP tool definitions.
func toolDefs() []gin.H {
	return []gin.H{
		{
			"name":        "store_messages",
			"description": "Store conversation messages in the episodic memory (immutable store)",
			"endpoint":    "/mcp/store_messages",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"session_id": gin.H{"type": "string", "description": "Conversation/session identifier"},
					"messages": gin.H{
						"type":        "array",
						"description": "Array of {role, content} message objects to store",
						"items": gin.H{
							"type": "object",
							"properties": gin.H{
								"role":    gin.H{"type": "string", "description": "Message role: user or assistant"},
								"content": gin.H{"type": "string", "description": "Message content"},
							},
							"required": []string{"role", "content"},
						},
					},
				},
				"required": []string{"session_id", "messages"},
			},
		},
		{
			"name":        "get_context",
			"description": "Assemble active context for a session — recent messages plus DAG summaries of older content",
			"endpoint":    "/mcp/get_context",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"session_id": gin.H{"type": "string", "description": "Conversation/session identifier"},
					"max_tokens": gin.H{"type": "integer", "description": "Maximum tokens for assembled context"},
				},
				"required": []string{"session_id"},
			},
		},
		{
			"name":        "search_messages",
			"description": "Full-text search across all stored messages and summaries",
			"endpoint":    "/mcp/search_messages",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"query":      gin.H{"type": "string", "description": "Search query"},
					"session_id": gin.H{"type": "string", "description": "Optional: limit search to a specific session"},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "expand_summary",
			"description": "Drill into a compressed summary to recover original message detail",
			"endpoint":    "/mcp/expand_summary",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"summary_id": gin.H{"type": "string", "description": "The summary node ID to expand"},
				},
				"required": []string{"summary_id"},
			},
		},
	}
}
