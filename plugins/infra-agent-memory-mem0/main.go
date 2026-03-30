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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-memory-mem0/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-memory-mem0/internal/memory"
)

// aliasTarget holds the resolved plugin ID and model for a given alias.
type aliasTarget struct {
	Plugin string
	Model  string
}

// llmResolution caches the resolved LLM target and persona system prompt.
type llmResolution struct {
	target       aliasTarget
	systemPrompt string
}

// pluginProxy holds the current resolved targets for LLM and embedder routing.
type pluginProxy struct {
	mu           sync.RWMutex
	llm          aliasTarget
	embedder     aliasTarget
	llmAliasName string // configured alias name (may be a persona like "brains")
	llmCache     *llmResolution // cached live resolution (nil = needs refresh)
	sdk          *pluginsdk.Client
}

func (p *pluginProxy) setTargets(llm, embedder aliasTarget) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.llm = llm
	p.embedder = embedder
}

func (p *pluginProxy) setLLMAliasName(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.llmAliasName = name
	p.llmCache = nil // invalidate on alias change
}

// invalidateLLMCache clears the cached persona/alias resolution so the next
// request will re-resolve from live data.
func (p *pluginProxy) invalidateLLMCache() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.llmCache = nil
	log.Printf("[proxy] LLM cache invalidated — next request will re-resolve")
}

// resolveLLM returns the LLM target and persona system prompt.
// Uses cached data if available; otherwise resolves live and caches the result.
func (p *pluginProxy) resolveLLM() (aliasTarget, string) {
	p.mu.RLock()
	if p.llmCache != nil {
		cached := p.llmCache
		p.mu.RUnlock()
		return cached.target, cached.systemPrompt
	}
	aliasName := p.llmAliasName
	p.mu.RUnlock()

	if aliasName == "" {
		return p.getTarget("llm"), ""
	}

	// Resolve live.
	var resolution llmResolution
	persona := fetchPersona(p.sdk, aliasName)
	if persona != nil {
		resolution.systemPrompt = persona.SystemPrompt
		resolved, err := resolveAlias(p.sdk, persona.BackendAlias)
		if err != nil {
			log.Printf("[proxy] failed to resolve persona backend_alias %q: %v — falling back to cached target", persona.BackendAlias, err)
			resolution.target = p.getTarget("llm")
		} else {
			resolution.target = resolved
			log.Printf("[proxy] resolved persona %q → %s/%s (prompt %d chars)", aliasName, resolved.Plugin, resolved.Model, len(persona.SystemPrompt))
		}
	} else {
		// Not a persona — resolve as regular alias.
		resolved, err := resolveAlias(p.sdk, aliasName)
		if err != nil {
			log.Printf("[proxy] failed to resolve alias %q: %v — falling back to cached target", aliasName, err)
			resolution.target = p.getTarget("llm")
		} else {
			resolution.target = resolved
		}
	}

	// Cache the resolution.
	p.mu.Lock()
	p.llmCache = &resolution
	p.mu.Unlock()

	return resolution.target, resolution.systemPrompt
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

	mem0Port := 8010
	if v := pluginConfig["MEM0_PORT"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			mem0Port = n
		}
	}

	// Resolve alias selections → plugin+model targets.
	proxy := &pluginProxy{sdk: sdkClient}
	applyConfig(sdkClient, proxy, pluginConfig, port, mem0Port)

	// Invalidate LLM cache when personas or aliases change so we pick up
	// backend_alias or system prompt changes without restart.
	sdkClient.OnEvent("persona:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		proxy.invalidateLLMCache()
	}))
	sdkClient.OnEvent("alias:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		proxy.invalidateLLMCache()
	}))

	// Create the Mem0 provider pointing at the local Mem0 server managed by supervisord.
	provider := memory.NewMem0Provider(fmt.Sprintf("http://localhost:%d", mem0Port))

	router := gin.Default()
	h := handlers.New(provider)
	handlerRef = h

	// SDK helper handlers.
	router.GET("/schema", gin.WrapF(sdkClient.SchemaHandler()))
	router.POST("/events", gin.WrapF(sdkClient.EventHandler()))

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

	// Push tools to MCP server when it becomes available.
	sdkClient.WhenPluginAvailable("infra:mcp-server", func(p pluginsdk.PluginInfo) {
		if err := sdkClient.RegisterToolsWithMCP(p.ID, handlerRef.ToolDefs()); err != nil {
			log.Printf("failed to register tools with MCP: %v", err)
		}
	})

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

// fetchPersona checks if aliasName matches a persona and returns it.
// Returns nil if no persona matches.
func fetchPersona(sdk *pluginsdk.Client, aliasName string) *pluginsdk.PersonaInfo {
	personas, err := sdk.FetchPersonas()
	if err != nil {
		log.Printf("[config] failed to fetch personas: %v", err)
		return nil
	}
	for _, p := range personas {
		if strings.EqualFold(p.Alias, aliasName) {
			return &p
		}
	}
	return nil
}

// applyConfig resolves alias selections and writes mem0.env for the Python server.
// If aliases can't be resolved (e.g. alias-registry not ready yet), it schedules
// background retries so the plugin starts immediately and self-heals.
func applyConfig(sdk *pluginsdk.Client, proxy *pluginProxy, config map[string]string, sidecarPort, mem0Port int) {
	llmAlias := config["MEM0_LLM_ALIAS"]
	embedderAlias := config["MEM0_EMBEDDER_ALIAS"]

	var llm, embedder aliasTarget

	if llmAlias != "" {
		proxy.setLLMAliasName(llmAlias)

		persona := fetchPersona(sdk, llmAlias)
		if persona != nil {
			log.Printf("[config] persona %q detected (%d char prompt), resolving via backend_alias %q",
				llmAlias, len(persona.SystemPrompt), persona.BackendAlias)
			resolved, err := resolveAlias(sdk, persona.BackendAlias)
			if err != nil {
				log.Printf("[config] LLM alias %q not yet available: %v — will retry in background", persona.BackendAlias, err)
			} else {
				llm = resolved
			}
		} else {
			resolved, err := resolveAlias(sdk, llmAlias)
			if err != nil {
				log.Printf("[config] LLM alias %q not yet available: %v — will retry in background", llmAlias, err)
			} else {
				llm = resolved
			}
		}
	}

	if embedderAlias != "" {
		resolved, err := resolveAlias(sdk, embedderAlias)
		if err != nil {
			log.Printf("[config] embedder alias %q not yet available: %v — will retry in background", embedderAlias, err)
		} else {
			embedder = resolved
		}
	}

	// Apply whatever we resolved so far.
	if llm.Plugin != "" || embedder.Plugin != "" {
		proxy.setTargets(llm, embedder)
	}

	// Write env file with whatever we have (Python server can start with existing config).
	writeMemEnv(llm, embedder, config, sidecarPort, mem0Port)

	// If either alias is missing, retry in background until resolved.
	if llm.Plugin == "" || embedder.Plugin == "" {
		go retryAliasResolution(sdk, proxy, config, sidecarPort, mem0Port)
	}
}

// retryAliasResolution retries alias resolution with exponential backoff.
func retryAliasResolution(sdk *pluginsdk.Client, proxy *pluginProxy, config map[string]string, sidecarPort, mem0Port int) {
	llmAlias := config["MEM0_LLM_ALIAS"]
	embedderAlias := config["MEM0_EMBEDDER_ALIAS"]
	delay := 2 * time.Second

	for attempt := 1; attempt <= 30; attempt++ {
		time.Sleep(delay)
		if delay < 30*time.Second {
			delay = delay * 3 / 2 // 2s, 3s, 4.5s, 6.75s, ...
		}

		var llm, embedder aliasTarget
		allResolved := true

		if llmAlias != "" {
			persona := fetchPersona(sdk, llmAlias)
			target := llmAlias
			if persona != nil {
				target = persona.BackendAlias
			}
			resolved, err := resolveAlias(sdk, target)
			if err != nil {
				allResolved = false
			} else {
				llm = resolved
			}
		}

		if embedderAlias != "" {
			resolved, err := resolveAlias(sdk, embedderAlias)
			if err != nil {
				allResolved = false
			} else {
				embedder = resolved
			}
		}

		if llm.Plugin != "" || embedder.Plugin != "" {
			proxy.setTargets(llm, embedder)
			writeMemEnv(llm, embedder, config, sidecarPort, mem0Port)
			log.Printf("[config] alias retry %d: llm=%s/%s embedder=%s/%s",
				attempt, llm.Plugin, llm.Model, embedder.Plugin, embedder.Model)
		}

		if allResolved {
			log.Printf("[config] all aliases resolved on retry %d", attempt)
			return
		}
	}
	log.Printf("[config] WARNING: gave up retrying alias resolution after 30 attempts")
}

// writeMemEnv writes the mem0.env configuration file for the Python server.
func writeMemEnv(llm, embedder aliasTarget, config map[string]string, sidecarPort, mem0Port int) {
	localBase := fmt.Sprintf("http://localhost:%d", sidecarPort+1)
	env := map[string]string{
		"MEM0_LLM_PROVIDER":      "openai",
		"MEM0_LLM_MODEL":         llm.Model,
		"MEM0_LLM_BASE_URL":      localBase + "/memory-api/llm/v1",
		"MEM0_LLM_API_KEY":       "not-needed",
		"MEM0_EMBEDDER_PROVIDER":  "openai",
		"MEM0_EMBEDDER_MODEL":     embedder.Model,
		"MEM0_EMBEDDER_BASE_URL":  localBase + "/memory-api/embedder/v1",
		"MEM0_EMBEDDER_API_KEY":   "not-needed",
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
	} else if llm.Plugin != "" && embedder.Plugin != "" {
		log.Printf("[mem0] config: llm=%s/%s (alias=%s) embedder=%s/%s (alias=%s)",
			llm.Plugin, llm.Model, config["MEM0_LLM_ALIAS"],
			embedder.Plugin, embedder.Model, config["MEM0_EMBEDDER_ALIAS"])
	}
}

// proxyHandler forwards requests to the resolved plugin via RouteToPlugin.
// For "llm" role on chat completions: resolves persona live, injects system prompt,
// and flattens structured fact objects in responses to plain strings.
func proxyHandler(sdk *pluginsdk.Client, proxy *pluginProxy, role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		subPath := c.Param("path")
		if subPath == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing path"})
			return
		}

		isChatCompletion := role == "llm" && strings.HasSuffix(subPath, "/chat/completions")

		// Resolve target and system prompt. For LLM chat completions, resolve live
		// from the persona so changes take effect immediately without restart.
		var target aliasTarget
		var systemPrompt string
		if isChatCompletion {
			target, systemPrompt = proxy.resolveLLM()
		} else {
			target = proxy.getTarget(role)
		}

		if target.Plugin == "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": fmt.Sprintf("no %s alias configured", role)})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
		defer cancel()

		var body io.Reader
		if c.Request.Body != nil && c.Request.Method != http.MethodGet {
			if isChatCompletion && systemPrompt != "" {
				if modified, err := injectSystemPrompt(c.Request.Body, systemPrompt); err == nil {
					body = bytes.NewReader(modified)
				} else {
					log.Printf("[memory-api/llm] failed to inject system prompt: %v", err)
					body = c.Request.Body
				}
			} else {
				body = c.Request.Body
			}
		}

		respBody, err := sdk.RouteToPlugin(ctx, target.Plugin, c.Request.Method, "/v1"+subPath, body)
		if err != nil {
			log.Printf("[memory-api/%s] RouteToPlugin(%s, %s) failed: %v", role, target.Plugin, subPath, err)
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("failed to reach %s: %v", target.Plugin, err)})
			return
		}

		// Flatten structured fact objects in the response if persona prompt was used.
		if isChatCompletion && systemPrompt != "" {
			if flattened, err := flattenFactResponse(respBody); err == nil {
				respBody = flattened
			} else {
				log.Printf("[memory-api/llm] fact flattening skipped: %v", err)
			}
		}

		contentType := "application/json"
		if len(respBody) > 0 && respBody[0] != '{' && respBody[0] != '[' {
			contentType = "text/plain"
		}

		c.Data(http.StatusOK, contentType, respBody)
	}
}

// injectSystemPrompt replaces or prepends the system message in a chat completion request
// with the persona's system prompt. If personaPrompt is empty, returns the original body unchanged.
func injectSystemPrompt(body io.Reader, personaPrompt string) ([]byte, error) {
	if personaPrompt == "" {
		raw, err := io.ReadAll(body)
		return raw, err
	}

	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(raw, &req); err != nil {
		return raw, nil // not JSON, pass through
	}

	messages, ok := req["messages"].([]interface{})
	if !ok {
		return raw, nil
	}

	// Replace existing system message or prepend one.
	replaced := false
	for i, msg := range messages {
		m, ok := msg.(map[string]interface{})
		if !ok {
			continue
		}
		if m["role"] == "system" {
			m["content"] = personaPrompt
			messages[i] = m
			replaced = true
			break
		}
	}
	if !replaced {
		systemMsg := map[string]interface{}{
			"role":    "system",
			"content": personaPrompt,
		}
		messages = append([]interface{}{systemMsg}, messages...)
	}

	req["messages"] = messages
	return json.Marshal(req)
}

// flattenFactResponse inspects the LLM response content for structured fact objects
// (from @brains-style prompts) and flattens them to plain strings that Mem0 expects.
// Input format:  {"facts": [{"content": "fact text", "category": "...", ...}, ...]}
// Output format: {"facts": ["fact text", "fact text", ...]}
func flattenFactResponse(respBody []byte) ([]byte, error) {
	// Parse the chat completion response to extract the content.
	var resp map[string]interface{}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	choices, ok := resp["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid choice format")
	}

	message, ok := choice["message"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("no message in choice")
	}

	content, ok := message["content"].(string)
	if !ok || content == "" {
		return nil, fmt.Errorf("no content in message")
	}

	// Strip markdown code fences if present.
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) > 2 {
			lines = lines[1 : len(lines)-1] // remove first and last fence lines
			content = strings.Join(lines, "\n")
		}
	}

	// Parse the content as JSON to check for structured facts.
	var factsObj map[string]interface{}
	if err := json.Unmarshal([]byte(content), &factsObj); err != nil {
		return nil, fmt.Errorf("content is not JSON: %w", err)
	}

	factsRaw, ok := factsObj["facts"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("no facts array in content")
	}

	// Check if facts are already plain strings (no flattening needed).
	if len(factsRaw) > 0 {
		if _, isString := factsRaw[0].(string); isString {
			return nil, fmt.Errorf("facts are already plain strings")
		}
	}

	// Flatten structured objects to plain strings.
	var plainFacts []string
	for _, f := range factsRaw {
		obj, ok := f.(map[string]interface{})
		if !ok {
			continue
		}
		// Extract the "content" field from each fact object.
		if text, ok := obj["content"].(string); ok && text != "" {
			plainFacts = append(plainFacts, text)
		}
	}

	// Rebuild the content as Mem0-compatible JSON.
	newContent := map[string]interface{}{"facts": plainFacts}
	newContentBytes, err := json.Marshal(newContent)
	if err != nil {
		return nil, fmt.Errorf("marshal flattened facts: %w", err)
	}

	// Replace the content in the response.
	message["content"] = string(newContentBytes)
	choice["message"] = message
	choices[0] = choice
	resp["choices"] = choices

	log.Printf("[memory-api/llm] flattened %d structured facts to plain strings", len(plainFacts))
	return json.Marshal(resp)
}

// configOptionsHandler serves dynamic dropdown options for alias selection.
// LLM alias accepts any chat-capable plugin; embedder alias requires memory:extraction (embeddings support).
func configOptionsHandler(sdk *pluginsdk.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		field := c.Param("field")

		switch field {
		case "MEM0_LLM_ALIAS":
			// Show agent:chat aliases + personas (which have custom system prompts).
			options := fetchLLMOptions(sdk)
			c.JSON(http.StatusOK, gin.H{"options": options})
		case "MEM0_EMBEDDER_ALIAS":
			// Embeddings require a plugin that exposes /v1/embeddings.
			options := fetchAliasesByCapability(sdk, "memory:extraction")
			c.JSON(http.StatusOK, gin.H{"options": options})
		default:
			c.JSON(http.StatusOK, gin.H{"options": []string{}})
		}
	}
}

// fetchAliasesByCapability returns alias names whose plugin has the given capability.
func fetchAliasesByCapability(sdk *pluginsdk.Client, capability string) []string {
	// Step 1: get plugins with the requested capability.
	plugins, err := sdk.SearchPlugins(capability)
	if err != nil {
		log.Printf("[config-options] failed to search %s plugins: %v", capability, err)
		return []string{}
	}
	matchingPlugins := make(map[string]bool, len(plugins))
	for _, p := range plugins {
		matchingPlugins[p.ID] = true
	}

	// Step 2: fetch all aliases from the alias-registry.
	aliases, err := sdk.FetchAliases()
	if err != nil {
		log.Printf("[config-options] failed to fetch aliases: %v", err)
		return []string{}
	}

	// Step 3: filter aliases to those from matching plugins.
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
		if matchingPlugins[pluginID] {
			options = append(options, a.Name)
		}
	}

	return options
}

// fetchLLMOptions returns agent:chat aliases plus personas that resolve to agent:chat plugins.
func fetchLLMOptions(sdk *pluginsdk.Client) []string {
	// Get agent:chat aliases.
	chatAliases := fetchAliasesByCapability(sdk, "agent:chat")
	chatSet := make(map[string]bool, len(chatAliases))
	seen := make(map[string]bool, len(chatAliases))
	for _, a := range chatAliases {
		chatSet[strings.ToLower(a)] = true
		seen[strings.ToLower(a)] = true
	}

	// Get personas — only include those whose backend_alias resolves to an agent:chat plugin.
	personas, err := sdk.FetchPersonas()
	if err != nil {
		log.Printf("[config-options] failed to fetch personas: %v", err)
		return chatAliases
	}

	options := make([]string, 0, len(chatAliases)+len(personas))
	// Personas first — they're the interesting ones with custom prompts.
	for _, p := range personas {
		name := strings.ToLower(p.Alias)
		backend := strings.ToLower(p.BackendAlias)
		if name != "" && !seen[name] && chatSet[backend] {
			options = append(options, p.Alias)
			seen[name] = true
		}
	}
	options = append(options, chatAliases...)
	return options
}
