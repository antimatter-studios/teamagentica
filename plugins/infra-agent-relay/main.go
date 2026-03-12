package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-relay/internal/bridge"
	"github.com/gin-gonic/gin"
)

// workspaceConn tracks a bridge connection to an agent-driven workspace.
type workspaceConn struct {
	WorkspaceID string
	Client      *bridge.Client
}

// relay manages connections to agent-bridge instances across workspaces.
type relay struct {
	mu    sync.RWMutex
	conns map[string]*workspaceConn // workspaceID → connection
	sdk   *pluginsdk.Client
}

func newRelay(sdk *pluginsdk.Client) *relay {
	return &relay{
		conns: make(map[string]*workspaceConn),
		sdk:   sdk,
	}
}

// getOrConnect returns an existing connection or establishes a new one.
func (r *relay) getOrConnect(workspaceID, bridgeAddr string) (*bridge.Client, error) {
	r.mu.RLock()
	if wc, ok := r.conns[workspaceID]; ok {
		r.mu.RUnlock()
		return wc.Client, nil
	}
	r.mu.RUnlock()

	// Connect to agent-bridge.
	client := bridge.NewClient(bridgeAddr)
	if err := client.Connect(); err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.conns[workspaceID] = &workspaceConn{
		WorkspaceID: workspaceID,
		Client:      client,
	}
	r.mu.Unlock()

	return client, nil
}

// disconnect closes and removes a workspace connection.
func (r *relay) disconnect(workspaceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if wc, ok := r.conns[workspaceID]; ok {
		wc.Client.Close()
		delete(r.conns, workspaceID)
	}
}

// chatRequest is the incoming message format from messaging plugins.
type chatRequest struct {
	Message     string `json:"message"`
	WorkspaceID string `json:"workspace_id"`
	BridgeAddr  string `json:"bridge_addr"` // host:port of agent-bridge in the container
}

// chatResponse is the response sent back to messaging plugins.
type chatResponse struct {
	Response string `json:"response"`
}

// handleChat receives a message, forwards it to agent-bridge, waits for
// the response, and returns it.
func (r *relay) handleChat(c *gin.Context) {
	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.WorkspaceID == "" || req.BridgeAddr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace_id and bridge_addr required"})
		return
	}

	client, err := r.getOrConnect(req.WorkspaceID, req.BridgeAddr)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("connect: %v", err)})
		return
	}

	// Send prompt.
	_, err = client.SendPrompt(req.Message)
	if err != nil {
		r.disconnect(req.WorkspaceID)
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("send: %v", err)})
		return
	}

	// Wait for response.
	response, err := client.ReadResponse()
	if err != nil {
		r.disconnect(req.WorkspaceID)
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("response: %v", err)})
		return
	}

	c.JSON(http.StatusOK, chatResponse{Response: response})
}

// handleCommand sends a slash command to agent-bridge.
func (r *relay) handleCommand(c *gin.Context) {
	var req struct {
		WorkspaceID string `json:"workspace_id"`
		BridgeAddr  string `json:"bridge_addr"`
		Command     string `json:"command"` // e.g. "/reset" or "/session ws-123-456"
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	client, err := r.getOrConnect(req.WorkspaceID, req.BridgeAddr)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("connect: %v", err)})
		return
	}

	_, err = client.SendCommand(req.Command)
	if err != nil {
		r.disconnect(req.WorkspaceID)
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("send: %v", err)})
		return
	}

	response, err := client.ReadResponse()
	if err != nil {
		r.disconnect(req.WorkspaceID)
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("response: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"response": response})
}

// handleStatus returns the status of all active workspace connections.
func (r *relay) handleStatus(c *gin.Context) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	workspaces := make([]string, 0, len(r.conns))
	for id := range r.conns {
		workspaces = append(workspaces, id)
	}

	c.JSON(http.StatusOK, gin.H{
		"active_connections": len(r.conns),
		"workspaces":         workspaces,
	})
}

// handleDisconnect closes a workspace connection.
func (r *relay) handleDisconnect(c *gin.Context) {
	var req struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	r.disconnect(req.WorkspaceID)
	c.JSON(http.StatusOK, gin.H{"status": "disconnected"})
}

func main() {
	sdkCfg := pluginsdk.LoadConfig()

	port := 8081
	if p := os.Getenv("PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           "infra-agent-relay",
		Host:         getHostname(),
		Port:         port,
		Capabilities: []string{"infra:agent-relay"},
		Version:      "0.1.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{},
	})
	sdkClient.Start(context.Background())

	r := newRelay(sdkClient)

	router := gin.Default()

	// Health check.
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Chat endpoint — messaging plugins route here to talk to agent workspaces.
	router.POST("/chat", r.handleChat)

	// Command endpoint — send slash commands to agent-bridge.
	router.POST("/command", r.handleCommand)

	// Status endpoint — list active workspace connections.
	router.GET("/status", r.handleStatus)

	// Disconnect endpoint — close a workspace connection.
	router.POST("/disconnect", r.handleDisconnect)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "localhost"
	}
	return hostname
}

// Keep json import used.
var _ = json.Marshal
