package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-relay/internal/bridge"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-relay/internal/router"
	"github.com/gin-gonic/gin"
)

// relay is the central message routing service.
// Messaging plugins send messages here; the relay routes to the correct
// destination: an LLM agent plugin (via kernel) or a workspace bridge (via TCP).
type relay struct {
	mu     sync.RWMutex
	conns  map[string]*bridge.Client // workspaceID → TCP connection
	routes *router.Table
	sdk    *pluginsdk.Client
}

func newRelay(sdk *pluginsdk.Client) *relay {
	return &relay{
		conns:  make(map[string]*bridge.Client),
		routes: router.NewTable(),
		sdk:    sdk,
	}
}

// --- Chat endpoint: the main entry point for all messaging plugins ---

// relayRequest is the envelope from messaging plugins.
type relayRequest struct {
	SourcePlugin string   `json:"source_plugin"`       // e.g. "messaging-discord"
	ChannelID    string   `json:"channel_id"`           // channel/group/chat ID
	Message      string   `json:"message"`              // user's message text
	ImageURLs    []string `json:"image_urls,omitempty"` // attached media
}

// relayResponse is returned to messaging plugins.
type relayResponse struct {
	Response  string `json:"response"`            // the response text/content
	Responder string `json:"responder,omitempty"` // alias or plugin ID that responded
}

// handleChat is the single entry point for all messages from messaging plugins.
func (r *relay) handleChat(c *gin.Context) {
	var req relayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.SourcePlugin == "" || req.ChannelID == "" || req.Message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source_plugin, channel_id, and message required"})
		return
	}

	// 1. Check if this channel is mapped to a workspace bridge.
	if ws := r.routes.GetWorkspace(req.SourcePlugin, req.ChannelID); ws != nil {
		r.routeToWorkspace(c, ws, req)
		return
	}

	// 2. Check for @alias prefix in message.
	aliases := r.routes.Aliases()
	if aliases != nil && !aliases.IsEmpty() {
		result := aliases.Parse(req.Message)
		if result.Target != nil && result.Target.Type == alias.TargetAgent {
			if result.Remainder == "" {
				c.JSON(http.StatusOK, relayResponse{
					Response:  fmt.Sprintf("Usage: @%s <message>", result.Alias),
					Responder: result.Alias,
				})
				return
			}
			r.routeToAgent(c, result.Target.PluginID, result.Target.Model,
				result.Remainder, req.ImageURLs, false, result.Alias)
			return
		}
	}

	// 3. Route to coordinator agent for this source plugin.
	coordinator := r.routes.GetCoordinator(req.SourcePlugin)
	if coordinator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no coordinator configured for " + req.SourcePlugin})
		return
	}

	response, err := r.callAgent(coordinator.PluginID, coordinator.Model,
		req.Message, req.ImageURLs, true, "")
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("coordinator: %v", err)})
		return
	}

	// 4. Check if coordinator delegated via ROUTE:@alias.
	if delegatedAlias, delegatedMsg, ok := alias.ParseCoordinatorResponse(response); ok {
		if aliases != nil {
			if target := aliases.Resolve(delegatedAlias); target != nil && target.Type == alias.TargetAgent {
				delegatedResp, err := r.callAgent(target.PluginID, target.Model,
					delegatedMsg, nil, false, delegatedAlias)
				if err != nil {
					c.JSON(http.StatusOK, relayResponse{
						Response:  fmt.Sprintf("Failed to reach @%s: %v", delegatedAlias, err),
						Responder: delegatedAlias,
					})
					return
				}
				c.JSON(http.StatusOK, relayResponse{
					Response:  delegatedResp,
					Responder: delegatedAlias,
				})
				return
			}
		}
	}

	// Return coordinator's direct response.
	responderName := ""
	if aliases != nil {
		responderName = aliases.FindAliasByPluginID(coordinator.PluginID)
	}
	if responderName == "" {
		responderName = coordinator.PluginID
	}

	c.JSON(http.StatusOK, relayResponse{
		Response:  response,
		Responder: responderName,
	})
}

// routeToWorkspace forwards a message to a workspace bridge via TCP.
func (r *relay) routeToWorkspace(c *gin.Context, ws *router.WorkspaceRoute, req relayRequest) {
	client, err := r.getOrConnect(ws.WorkspaceID, ws.BridgeAddr)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("connect: %v", err)})
		return
	}

	_, err = client.SendPrompt(req.Message)
	if err != nil {
		r.disconnect(ws.WorkspaceID)
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("send: %v", err)})
		return
	}

	response, err := client.ReadResponse()
	if err != nil {
		r.disconnect(ws.WorkspaceID)
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("response: %v", err)})
		return
	}

	c.JSON(http.StatusOK, relayResponse{
		Response:  response,
		Responder: "workspace:" + ws.WorkspaceID,
	})
}

// routeToAgent forwards a message to an agent plugin and returns the response.
func (r *relay) routeToAgent(c *gin.Context, pluginID, model, message string, imageURLs []string, isCoordinator bool, agentAlias string) {
	response, err := r.callAgent(pluginID, model, message, imageURLs, isCoordinator, agentAlias)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("agent: %v", err)})
		return
	}

	c.JSON(http.StatusOK, relayResponse{
		Response:  response,
		Responder: agentAlias,
	})
}

// agentChatRequest is the standard chat format used by all agent plugins.
type agentChatRequest struct {
	Message       string             `json:"message"`
	Model         string             `json:"model,omitempty"`
	ImageURLs     []string           `json:"image_urls,omitempty"`
	Conversation  []conversationMsg  `json:"conversation"`
	IsCoordinator bool               `json:"is_coordinator,omitempty"`
	AgentAlias    string             `json:"agent_alias,omitempty"`
}

type conversationMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type agentChatResponse struct {
	Response string `json:"response"`
}

// callAgent sends a chat request to an agent plugin via the kernel route.
func (r *relay) callAgent(pluginID, model, message string, imageURLs []string, isCoordinator bool, agentAlias string) (string, error) {
	reqBody := agentChatRequest{
		Message:       message,
		Model:         model,
		ImageURLs:     imageURLs,
		Conversation:  []conversationMsg{{Role: "user", Content: message}},
		IsCoordinator: isCoordinator,
		AgentAlias:    agentAlias,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	respBody, err := r.sdk.RouteToPlugin(context.Background(), pluginID, "POST", "/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	var chatResp agentChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	return chatResp.Response, nil
}

// --- Workspace connection management ---

func (r *relay) getOrConnect(workspaceID, bridgeAddr string) (*bridge.Client, error) {
	r.mu.RLock()
	if client, ok := r.conns[workspaceID]; ok {
		r.mu.RUnlock()
		return client, nil
	}
	r.mu.RUnlock()

	client := bridge.NewClient(bridgeAddr)
	if err := client.Connect(); err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.conns[workspaceID] = client
	r.mu.Unlock()

	return client, nil
}

func (r *relay) disconnect(workspaceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if client, ok := r.conns[workspaceID]; ok {
		client.Close()
		delete(r.conns, workspaceID)
	}
}

// --- Config & routing management endpoints ---

// handleSetCoordinator sets the coordinator agent for a source plugin.
// Accepts either plugin_id directly or alias (resolved from the alias map).
func (r *relay) handleSetCoordinator(c *gin.Context) {
	var req struct {
		SourcePlugin string `json:"source_plugin"`
		PluginID     string `json:"plugin_id,omitempty"`
		Alias        string `json:"alias,omitempty"`
		Model        string `json:"model,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.SourcePlugin == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source_plugin required"})
		return
	}

	pluginID := req.PluginID
	model := req.Model

	// If alias is provided, resolve it to a plugin ID.
	if req.Alias != "" && pluginID == "" {
		aliases := r.routes.Aliases()
		if aliases == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "aliases not loaded yet"})
			return
		}
		target := aliases.Resolve(req.Alias)
		if target == nil || target.Type != alias.TargetAgent {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("alias @%s not found or not an agent", req.Alias)})
			return
		}
		pluginID = target.PluginID
		if model == "" {
			model = target.Model
		}
	}

	if pluginID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "plugin_id or alias required"})
		return
	}

	r.routes.SetCoordinator(req.SourcePlugin, pluginID, model)
	log.Printf("coordinator set: %s → %s (model=%s, alias=%s)", req.SourcePlugin, pluginID, model, req.Alias)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "plugin_id": pluginID, "alias": req.Alias})
}

// handleMapWorkspace maps a channel to a workspace bridge.
func (r *relay) handleMapWorkspace(c *gin.Context) {
	var req struct {
		SourcePlugin string `json:"source_plugin"`
		ChannelID    string `json:"channel_id"`
		WorkspaceID  string `json:"workspace_id"`
		BridgeAddr   string `json:"bridge_addr"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	r.routes.MapWorkspace(req.SourcePlugin, req.ChannelID, req.WorkspaceID, req.BridgeAddr)
	log.Printf("workspace mapped: %s/%s → %s at %s", req.SourcePlugin, req.ChannelID, req.WorkspaceID, req.BridgeAddr)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// handleUnmapWorkspace removes a channel→workspace mapping.
func (r *relay) handleUnmapWorkspace(c *gin.Context) {
	var req struct {
		SourcePlugin string `json:"source_plugin"`
		ChannelID    string `json:"channel_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	r.routes.UnmapWorkspace(req.SourcePlugin, req.ChannelID)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// handleStatus returns the relay's routing state.
func (r *relay) handleStatus(c *gin.Context) {
	r.mu.RLock()
	workspaces := make([]string, 0, len(r.conns))
	for id := range r.conns {
		workspaces = append(workspaces, id)
	}
	r.mu.RUnlock()

	c.JSON(http.StatusOK, gin.H{
		"active_connections": len(workspaces),
		"workspaces":         workspaces,
		"coordinators":       r.routes.ListCoordinators(),
		"workspace_mappings": r.routes.ListWorkspaces(),
	})
}

func main() {
	sdkCfg := pluginsdk.LoadConfig()
	manifest := pluginsdk.LoadManifest()

	port := 8081
	if p := os.Getenv("PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         getHostname(),
		Port:         port,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		ConfigSchema: manifest.ConfigSchema,
	})
	r := newRelay(sdkClient)

	// Subscribe to alias updates from kernel (before Start).
	sdkClient.OnEvent("kernel:alias:update", pluginsdk.NewTimedDebouncer(2*time.Second, func(event pluginsdk.EventCallback) {
		var detail struct {
			Aliases []alias.AliasInfo `json:"aliases"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("alias update parse error: %v", err)
			return
		}
		r.routes.SetAliases(alias.NewAliasMap(detail.Aliases))
		log.Printf("Aliases updated: %d entries", len(detail.Aliases))
	}))

	sdkClient.Start(context.Background())

	// Fetch initial aliases.
	entries, err := sdkClient.FetchAliases()
	if err != nil {
		log.Printf("Initial alias fetch failed: %v (will update via events)", err)
	} else {
		r.routes.SetAliases(alias.NewAliasMap(entries))
		log.Printf("Loaded %d aliases", len(entries))
	}

	ginRouter := gin.Default()

	// Health check.
	ginRouter.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Main chat endpoint — all messaging plugins route here.
	ginRouter.POST("/chat", r.handleChat)

	// Routing config endpoints.
	ginRouter.POST("/config/coordinator", r.handleSetCoordinator)
	ginRouter.POST("/config/workspace/map", r.handleMapWorkspace)
	ginRouter.POST("/config/workspace/unmap", r.handleUnmapWorkspace)

	// Status endpoint.
	ginRouter.GET("/status", r.handleStatus)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: ginRouter,
	}

	// Broadcast that the relay is ready — messaging plugins use this to
	// (re)send their coordinator assignments after a relay restart.
	go func() {
		// Small delay to ensure the HTTP server is accepting connections.
		time.Sleep(500 * time.Millisecond)
		sdkClient.ReportEvent("relay:ready", "accepting coordinator and chat requests")
	}()

	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "localhost"
	}
	return hostname
}
