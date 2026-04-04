package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

const (
	semanticPlugin = "infra-agent-memory-mem0"
	episodicPlugin = "infra-agent-memory-lcm"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()
	hostname, _ := os.Hostname()
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8091

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		SchemaFunc: func() map[string]interface{} {
			return map[string]interface{}{
				"config": manifest.ConfigSchema,
				"backends": map[string]interface{}{
					"semantic": semanticPlugin,
					"episodic": episodicPlugin,
				},
			}
		},
		ToolsFunc: func() interface{} {
			return toolDefs()
		},
	})

	ctx := context.Background()
	sdkClient.Start(ctx)

	router := gin.Default()

	// Health — check both backends
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy", "role": "gateway"})
	})

	// MCP tool discovery
	router.GET("/mcp", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"tools": toolDefs()})
	})

	// Semantic memory tools — proxy to infra-agent-memory-mem0
	router.POST("/mcp/add_memory", proxyToBackend(sdkClient, semanticPlugin, "/mcp/add_memory"))
	router.POST("/mcp/search_memories", proxyToBackend(sdkClient, semanticPlugin, "/mcp/search_memories"))
	router.POST("/mcp/get_memories", proxyToBackend(sdkClient, semanticPlugin, "/mcp/get_memories"))
	router.POST("/mcp/get_memory", proxyToBackend(sdkClient, semanticPlugin, "/mcp/get_memory"))
	router.POST("/mcp/update_memory", proxyToBackend(sdkClient, semanticPlugin, "/mcp/update_memory"))
	router.POST("/mcp/delete_memory", proxyToBackend(sdkClient, semanticPlugin, "/mcp/delete_memory"))
	router.POST("/mcp/delete_all_memories", proxyToBackend(sdkClient, semanticPlugin, "/mcp/delete_all_memories"))
	router.POST("/mcp/delete_entities", proxyToBackend(sdkClient, semanticPlugin, "/mcp/delete_entities"))
	router.POST("/mcp/list_entities", proxyToBackend(sdkClient, semanticPlugin, "/mcp/list_entities"))

	// Episodic memory tools — proxy to infra-agent-memory-lcm
	router.POST("/mcp/store_messages", proxyToBackend(sdkClient, episodicPlugin, "/mcp/store_messages"))
	router.POST("/mcp/get_context", proxyToBackend(sdkClient, episodicPlugin, "/mcp/get_context"))
	router.POST("/mcp/search_messages", proxyToBackend(sdkClient, episodicPlugin, "/mcp/search_messages"))
	router.POST("/mcp/expand_summary", proxyToBackend(sdkClient, episodicPlugin, "/mcp/expand_summary"))

	// Episodic browsing — proxy GET requests to LCM
	router.GET("/conversations", proxyGetToBackend(sdkClient, episodicPlugin, "/conversations"))
	router.GET("/conversations/:id/messages", func(c *gin.Context) {
		id := c.Param("id")
		query := ""
		if c.Request.URL.RawQuery != "" {
			query = "?" + c.Request.URL.RawQuery
		}
		path := "/conversations/" + id + "/messages" + query
		proxyGetToBackend(sdkClient, episodicPlugin, path)(c)
	})

	// Dynamic config options — proxy to mem0 backend
	router.GET("/config/options/:field", func(c *gin.Context) {
		field := c.Param("field")
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()

		body, err := sdkClient.RouteToPlugin(ctx, semanticPlugin, "GET", "/config/options/"+field, nil)
		if err != nil {
			log.Printf("[gateway] config options proxy error: %v", err)
			c.JSON(http.StatusOK, gin.H{"options": []string{}})
			return
		}

		c.Data(http.StatusOK, "application/json", body)
	})

	// Push tools to MCP server when it becomes available.
	sdkClient.OnPluginAvailable("infra:mcp-server", func(p pluginsdk.PluginInfo) {
		if err := sdkClient.RegisterToolsWithMCP(p.ID, toolDefs()); err != nil {
			log.Printf("failed to register tools with MCP: %v", err)
		}
	})

	sdkClient.ListenAndServe(8091, router)
}

// proxyToBackend creates a handler that forwards the request body to a backend plugin.
func proxyToBackend(sdk *pluginsdk.Client, pluginID, path string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
		defer cancel()

		var body io.Reader
		if c.Request.Body != nil {
			body = c.Request.Body
		}

		respBody, err := sdk.RouteToPlugin(ctx, pluginID, "POST", path, body)
		if err != nil {
			log.Printf("[gateway] proxy to %s%s failed: %v", pluginID, path, err)
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}

		c.Data(http.StatusOK, "application/json", respBody)
	}
}

// proxyGetToBackend creates a handler that forwards a GET request to a backend plugin.
func proxyGetToBackend(sdk *pluginsdk.Client, pluginID, path string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()

		respBody, err := sdk.RouteToPlugin(ctx, pluginID, "GET", path, nil)
		if err != nil {
			log.Printf("[gateway] GET proxy to %s%s failed: %v", pluginID, path, err)
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}

		c.Data(http.StatusOK, "application/json", respBody)
	}
}

// toolDefs returns the unified MCP tool definitions combining semantic + episodic.
func toolDefs() []gin.H {
	return []gin.H{
		// -- Semantic (Mem0) tools --
		{
			"name":        "add_memory",
			"description": "Save a conversation or text to semantic memory. Automatically extracts important facts.",
			"endpoint":    "/mcp/add_memory",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"messages": gin.H{
						"type":        "array",
						"description": "Array of {role, content} message objects to extract memories from",
						"items": gin.H{
							"type": "object",
							"properties": gin.H{
								"role":    gin.H{"type": "string", "description": "Message role: user or assistant"},
								"content": gin.H{"type": "string", "description": "Message content"},
							},
							"required": []string{"role", "content"},
						},
					},
					"user_id":             gin.H{"type": "string", "description": "User scope (default: global)"},
					"agent_id":            gin.H{"type": "string", "description": "Agent scope"},
					"run_id":              gin.H{"type": "string", "description": "Run/session scope"},
					"metadata":            gin.H{"type": "object", "description": "Arbitrary metadata to attach"},
					"infer":               gin.H{"type": "boolean", "description": "Extract facts from messages (default: true)"},
					"immutable":           gin.H{"type": "boolean", "description": "If true, memory cannot be updated later"},
					"custom_categories":   gin.H{"type": "array", "items": gin.H{"type": "string"}, "description": "Constrain extraction to these categories"},
					"custom_instructions": gin.H{"type": "string", "description": "Extra guidance for the extraction LLM"},
				},
				"required": []string{"messages"},
			},
		},
		{
			"name":        "search_memories",
			"description": "Semantic search across stored memories. Returns the most relevant memories for a query.",
			"endpoint":    "/mcp/search_memories",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"query":          gin.H{"type": "string", "description": "Natural language search query"},
					"user_id":        gin.H{"type": "string", "description": "Filter by user (default: global)"},
					"agent_id":       gin.H{"type": "string", "description": "Filter by agent"},
					"run_id":         gin.H{"type": "string", "description": "Filter by run/session"},
					"top_k":          gin.H{"type": "integer", "description": "Max results to return (default: 10)"},
					"threshold":      gin.H{"type": "number", "description": "Minimum similarity score (0-1)"},
					"rerank":         gin.H{"type": "boolean", "description": "Re-rank results for better relevance"},
					"keyword_search": gin.H{"type": "boolean", "description": "Also include keyword-based matches"},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "get_memories",
			"description": "List all semantic memories with optional filters and pagination.",
			"endpoint":    "/mcp/get_memories",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"user_id":   gin.H{"type": "string", "description": "Filter by user (default: global)"},
					"agent_id":  gin.H{"type": "string", "description": "Filter by agent"},
					"run_id":    gin.H{"type": "string", "description": "Filter by run/session"},
					"page":      gin.H{"type": "integer", "description": "Page number for pagination"},
					"page_size": gin.H{"type": "integer", "description": "Results per page (default: 50)"},
				},
			},
		},
		{
			"name":        "get_memory",
			"description": "Retrieve a single semantic memory by its ID.",
			"endpoint":    "/mcp/get_memory",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"memory_id": gin.H{"type": "string", "description": "The memory ID to retrieve"},
				},
				"required": []string{"memory_id"},
			},
		},
		{
			"name":        "update_memory",
			"description": "Update an existing memory's text or metadata.",
			"endpoint":    "/mcp/update_memory",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"memory_id": gin.H{"type": "string", "description": "The memory ID to update"},
					"text":      gin.H{"type": "string", "description": "New text content"},
					"metadata":  gin.H{"type": "object", "description": "Updated metadata"},
				},
				"required": []string{"memory_id"},
			},
		},
		{
			"name":        "delete_memory",
			"description": "Delete a single semantic memory by ID.",
			"endpoint":    "/mcp/delete_memory",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"memory_id": gin.H{"type": "string", "description": "The memory ID to delete"},
				},
				"required": []string{"memory_id"},
			},
		},
		{
			"name":        "delete_all_memories",
			"description": "Delete all semantic memories for a given scope. At least one scope filter is required.",
			"endpoint":    "/mcp/delete_all_memories",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"user_id":  gin.H{"type": "string", "description": "Delete all memories for this user"},
					"agent_id": gin.H{"type": "string", "description": "Delete all memories for this agent"},
					"app_id":   gin.H{"type": "string", "description": "Delete all memories for this app"},
					"run_id":   gin.H{"type": "string", "description": "Delete all memories for this run"},
				},
			},
		},
		{
			"name":        "delete_entities",
			"description": "Hard-delete an entity and all its associated memories.",
			"endpoint":    "/mcp/delete_entities",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"entity_type": gin.H{"type": "string", "enum": []string{"user", "agent", "app", "run"}, "description": "Entity type"},
					"entity_id":   gin.H{"type": "string", "description": "Entity ID to delete"},
				},
				"required": []string{"entity_type", "entity_id"},
			},
		},
		{
			"name":        "list_entities",
			"description": "List all known entities in the semantic memory system.",
			"endpoint":    "/mcp/list_entities",
			"parameters":  gin.H{"type": "object", "properties": gin.H{}},
		},
		// -- Episodic (LCM) tools --
		{
			"name":        "store_messages",
			"description": "Store conversation messages in the episodic memory (immutable store).",
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
			"description": "Assemble active context for a session — recent messages plus DAG summaries of older content.",
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
			"description": "Full-text search across all stored conversation messages and summaries.",
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
			"description": "Drill into a compressed summary to recover original message detail.",
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
