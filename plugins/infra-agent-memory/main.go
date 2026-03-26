package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-memory/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-memory/internal/memory"
)

// aliasTarget holds the resolved plugin ID and model for a given alias.
type aliasTarget struct {
	Plugin string
	Model  string
}

// pluginProxy holds the current resolved targets for LLM and embedder routing.
type pluginProxy struct {
	mu       sync.RWMutex
	llm      aliasTarget
	embedder aliasTarget
}

func (p *pluginProxy) setTargets(llm, embedder aliasTarget) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.llm = llm
	p.embedder = embedder
}

func (p *pluginProxy) getTarget(role string) aliasTarget {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if role == "llm" {
		return p.llm
	}
	return p.embedder
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()
	hostname, _ := os.Hostname()
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8091

	var handlerRef *handlers.Handler

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
			if handlerRef != nil {
				schema["memory_stats"] = handlerRef.MemoryStats()
				list, total, displayed := handlerRef.MemoryList()
				schema[fmt.Sprintf("memories (%d/%d)", displayed, total)] = list
			}
			return schema
		},
		ToolsFunc: func() interface{} {
			if handlerRef != nil {
				return handlerRef.ToolDefs()
			}
			return nil
		},
	})

	ctx := context.Background()
	sdkClient.Start(ctx)

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("failed to fetch plugin config: %v", err)
	}

	port := defaultPort
	if v := pluginConfig["PLUGIN_PORT"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}

	mem0Port := 8010
	if v := pluginConfig["MEM0_PORT"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			mem0Port = n
		}
	}

	// Resolve alias selections → plugin+model targets.
	proxy := &pluginProxy{}
	applyConfig(sdkClient, proxy, pluginConfig, port, mem0Port)

	// Create the Mem0 provider pointing at the local Mem0 server managed by supervisord.
	provider := memory.NewMem0Provider(fmt.Sprintf("http://localhost:%d", mem0Port))

	router := gin.Default()
	h := handlers.New(provider)
	handlerRef = h

	// Health
	router.GET("/health", h.Health)

	// Dynamic config options — alias dropdowns filtered by memory:extraction capability.
	router.GET("/config/options/:field", configOptionsHandler(sdkClient))

	// MCP tool discovery + execution
	router.GET("/mcp", h.GetTools)
	router.POST("/mcp/add_memory", h.MCPAddMemory)
	router.POST("/mcp/search_memories", h.MCPSearchMemories)
	router.POST("/mcp/get_memories", h.MCPGetMemories)
	router.POST("/mcp/get_memory", h.MCPGetMemory)
	router.POST("/mcp/update_memory", h.MCPUpdateMemory)
	router.POST("/mcp/delete_memory", h.MCPDeleteMemory)
	router.POST("/mcp/delete_all_memories", h.MCPDeleteAllMemories)
	router.POST("/mcp/delete_entities", h.MCPDeleteEntities)
	router.POST("/mcp/list_entities", h.MCPListEntities)

	// Internal plain-HTTP proxy for Mem0 Python → agent plugins.
	// The main server uses mTLS, so Mem0's OpenAI SDK can't talk to it directly.
	// This separate listener on proxyPort serves only the memory-api routes over plain HTTP.
	proxyPort := port + 1 // 8092
	proxyRouter := gin.Default()
	proxyRouter.Any("/memory-api/llm/*path", proxyHandler(sdkClient, proxy, "llm"))
	proxyRouter.Any("/memory-api/embedder/*path", proxyHandler(sdkClient, proxy, "embedder"))
	go func() {
		proxyAddr := fmt.Sprintf(":%d", proxyPort)
		log.Printf("[memory-api] internal HTTP proxy listening on %s", proxyAddr)
		if err := http.ListenAndServe(proxyAddr, proxyRouter); err != nil {
			log.Fatalf("[memory-api] proxy server error: %v", err)
		}
	}()

	// Hot-reload config: resolve aliases again and tell Mem0 to reinitialize.
	mem0URL := fmt.Sprintf("http://localhost:%d", mem0Port)
	sdkClient.OnEvent("config:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var detail struct {
			Config map[string]string `json:"config"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("[config] failed to parse config:update: %v", err)
			return
		}
		for k, v := range detail.Config {
			pluginConfig[k] = v
		}
		applyConfig(sdkClient, proxy, detail.Config, port, mem0Port)

		resp, err := http.Post(mem0URL+"/reload", "application/json", nil)
		if err != nil {
			log.Printf("[config] failed to trigger Mem0 reload: %v", err)
			return
		}
		resp.Body.Close()
		log.Printf("[config] Mem0 reload triggered (status %d)", resp.StatusCode)
	}))

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}

// resolveAlias fetches an alias from the alias-registry and returns plugin+model.
func resolveAlias(sdk *pluginsdk.Client, aliasName string) (aliasTarget, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	body, err := sdk.RouteToPlugin(ctx, "infra-alias-registry", "GET", "/alias/"+aliasName, nil)
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

// applyConfig resolves alias selections and writes mem0.env for the Python server.
func applyConfig(sdk *pluginsdk.Client, proxy *pluginProxy, config map[string]string, sidecarPort, mem0Port int) {
	llmAlias := config["MEM0_LLM_ALIAS"]
	embedderAlias := config["MEM0_EMBEDDER_ALIAS"]

	var llm, embedder aliasTarget

	if llmAlias != "" {
		resolved, err := resolveAlias(sdk, llmAlias)
		if err != nil {
			log.Printf("[config] failed to resolve LLM alias %q: %v", llmAlias, err)
		} else {
			llm = resolved
		}
	}

	if embedderAlias != "" {
		resolved, err := resolveAlias(sdk, embedderAlias)
		if err != nil {
			log.Printf("[config] failed to resolve embedder alias %q: %v", embedderAlias, err)
		} else {
			embedder = resolved
		}
	}

	if llm.Plugin == "" {
		log.Printf("[config] ERROR: no LLM configured — set MEM0_LLM_ALIAS in plugin config")
		return
	}
	if embedder.Plugin == "" {
		log.Printf("[config] ERROR: no embedder configured — set MEM0_EMBEDDER_ALIAS in plugin config")
		return
	}

	proxy.setTargets(llm, embedder)

	// Mem0 uses "openai" provider pointing at the internal plain-HTTP proxy
	// (sidecarPort+1), not the mTLS main port.
	localBase := fmt.Sprintf("http://localhost:%d", sidecarPort+1)
	env := map[string]string{
		"MEM0_LLM_PROVIDER":     "openai",
		"MEM0_LLM_MODEL":        llm.Model,
		"MEM0_LLM_BASE_URL":     localBase + "/memory-api/llm/v1",
		"MEM0_LLM_API_KEY":      "not-needed",
		"MEM0_EMBEDDER_PROVIDER": "openai",
		"MEM0_EMBEDDER_MODEL":    embedder.Model,
		"MEM0_EMBEDDER_BASE_URL": localBase + "/memory-api/embedder/v1",
		"MEM0_EMBEDDER_API_KEY":  "not-needed",
	}

	var lines []string
	for k, v := range env {
		lines = append(lines, k+"="+v)
	}
	lines = append(lines, fmt.Sprintf("MEM0_PORT=%d", mem0Port))

	content := ""
	for _, l := range lines {
		content += l + "\n"
	}

	if err := os.WriteFile("/data/mem0.env", []byte(content), 0644); err != nil {
		log.Printf("WARNING: failed to write /data/mem0.env: %v", err)
	} else {
		log.Printf("[mem0] config: llm=%s/%s (alias=%s) embedder=%s/%s (alias=%s)",
			llm.Plugin, llm.Model, config["MEM0_LLM_ALIAS"],
			embedder.Plugin, embedder.Model, config["MEM0_EMBEDDER_ALIAS"])
	}
}

// proxyHandler forwards requests to the resolved plugin via RouteToPlugin.
func proxyHandler(sdk *pluginsdk.Client, proxy *pluginProxy, role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		target := proxy.getTarget(role)
		if target.Plugin == "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": fmt.Sprintf("no %s alias configured", role)})
			return
		}

		subPath := c.Param("path")
		if subPath == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing path"})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
		defer cancel()

		var body io.Reader
		if c.Request.Body != nil && c.Request.Method != http.MethodGet {
			body = c.Request.Body
		}

		respBody, err := sdk.RouteToPlugin(ctx, target.Plugin, c.Request.Method, "/v1"+subPath, body)
		if err != nil {
			log.Printf("[memory-api/%s] RouteToPlugin(%s, %s) failed: %v", role, target.Plugin, subPath, err)
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("failed to reach %s: %v", target.Plugin, err)})
			return
		}

		contentType := "application/json"
		if len(respBody) > 0 && respBody[0] != '{' && respBody[0] != '[' {
			contentType = "text/plain"
		}

		c.Data(http.StatusOK, contentType, respBody)
	}
}

// configOptionsHandler serves dynamic dropdown options for alias selection.
// Fetches all aliases, then filters to only those from plugins with memory:extraction capability.
func configOptionsHandler(sdk *pluginsdk.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		field := c.Param("field")

		switch field {
		case "MEM0_LLM_ALIAS", "MEM0_EMBEDDER_ALIAS":
			options := fetchMemoryAliases(sdk)
			c.JSON(http.StatusOK, gin.H{"options": options})
		default:
			c.JSON(http.StatusOK, gin.H{"options": []string{}})
		}
	}
}

// fetchMemoryAliases returns alias names whose plugin has the memory:extraction capability.
func fetchMemoryAliases(sdk *pluginsdk.Client) []string {
	// Step 1: get plugins with memory:extraction capability.
	plugins, err := sdk.SearchPlugins("memory:extraction")
	if err != nil {
		log.Printf("[config-options] failed to search memory:extraction plugins: %v", err)
		return []string{}
	}
	memoryPlugins := make(map[string]bool, len(plugins))
	for _, p := range plugins {
		memoryPlugins[p.ID] = true
	}

	// Step 2: fetch all aliases from the alias-registry.
	aliases, err := sdk.FetchAliases()
	if err != nil {
		log.Printf("[config-options] failed to fetch aliases: %v", err)
		return []string{}
	}

	// Step 3: filter aliases to those from memory:extraction plugins.
	var options []string
	for _, a := range aliases {
		// AliasInfo.Target is "plugin-id:model" — extract plugin ID.
		pluginID := a.Target
		for i, ch := range a.Target {
			if ch == ':' {
				pluginID = a.Target[:i]
				break
			}
		}
		if memoryPlugins[pluginID] {
			options = append(options, a.Name)
		}
	}

	return options
}
