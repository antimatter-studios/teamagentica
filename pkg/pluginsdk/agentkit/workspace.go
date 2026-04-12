// Package agentkit workspace helpers provide SDK-level workspace lifecycle
// management so agents can programmatically create, exec into, and destroy
// workspaces via the workspace-manager plugin API and exec-server WebSocket.
package agentkit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

// WorkspaceClient manages workspace lifecycle for agents.
type WorkspaceClient struct {
	sdkClient *pluginsdk.Client

	// Track active workspaces for cleanup.
	mu         sync.Mutex
	workspaces map[string]*Workspace
}

// NewWorkspaceClient creates a workspace client.
func NewWorkspaceClient(client *pluginsdk.Client) *WorkspaceClient {
	return &WorkspaceClient{
		sdkClient:  client,
		workspaces: make(map[string]*Workspace),
	}
}

// WorkspaceConfig describes what the agent needs for a workspace.
type WorkspaceConfig struct {
	Name          string            // human-friendly name
	EnvironmentID string            // workspace environment plugin ID (e.g. "workspace-env-devbox")
	Disks         []DiskMount       // extra disks to mount
	EnvVars       map[string]string // extra environment variables
	GitRepo       string            // optional: clone this repo into workspace
	GitRef        string            // optional: checkout this ref after clone
}

// DiskMount describes a disk to mount in the workspace.
type DiskMount struct {
	Name     string // disk name in storage-disk
	Type     string // "shared" or "workspace"
	Target   string // mount path inside workspace
	ReadOnly bool
}

// Workspace represents a running workspace.
type Workspace struct {
	ID          string // managed container ID
	Name        string
	Subdomain   string
	Status      string
	ContainerID string // Docker container ID (hostname for networking)
	ExecURL     string // WebSocket URL for exec-server
	DiskName    string // primary workspace disk name
}

// createWorkspaceRequest matches the workspace-manager POST /workspaces body.
type createWorkspaceRequest struct {
	Name          string `json:"name"`
	EnvironmentID string `json:"environment_id"`
	GitRepo       string `json:"git_repo,omitempty"`
	GitRef        string `json:"git_ref,omitempty"`
}

// createWorkspaceResponse matches workspace-manager's response.
type createWorkspaceResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Subdomain string `json:"subdomain"`
	DiskName  string `json:"disk_name"`
}

// Create creates a new workspace via workspace-manager.
func (wc *WorkspaceClient) Create(ctx context.Context, cfg WorkspaceConfig) (*Workspace, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("workspace name is required")
	}
	if cfg.EnvironmentID == "" {
		return nil, fmt.Errorf("environment_id is required")
	}

	reqBody, _ := json.Marshal(createWorkspaceRequest{
		Name:          cfg.Name,
		EnvironmentID: cfg.EnvironmentID,
		GitRepo:       cfg.GitRepo,
		GitRef:        cfg.GitRef,
	})

	data, err := wc.sdkClient.RouteToPlugin(ctx, "workspace-manager", "POST", "/workspaces", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}

	var resp createWorkspaceResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode workspace response: %w", err)
	}

	ws := &Workspace{
		ID:          resp.ID,
		Name:        resp.Name,
		Subdomain:   resp.Subdomain,
		Status:      resp.Status,
		ContainerID: resp.ID, // MC ID is used as container hostname prefix
		DiskName:    resp.DiskName,
	}
	// Build exec-server WebSocket URL using the Docker container naming convention.
	ws.ExecURL = fmt.Sprintf("ws://teamagentica-mc-%s:9100/exec", resp.ID)

	wc.mu.Lock()
	wc.workspaces[ws.ID] = ws
	wc.mu.Unlock()

	log.Printf("agentkit/workspace: created %s (id=%s, subdomain=%s)", ws.Name, ws.ID, ws.Subdomain)
	return ws, nil
}

// Get retrieves workspace details by ID.
func (wc *WorkspaceClient) Get(ctx context.Context, id string) (*Workspace, error) {
	data, err := wc.sdkClient.RouteToPlugin(ctx, "workspace-manager", "GET", "/workspaces/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("get workspace: %w", err)
	}

	var resp createWorkspaceResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode workspace response: %w", err)
	}

	return &Workspace{
		ID:          resp.ID,
		Name:        resp.Name,
		Subdomain:   resp.Subdomain,
		Status:      resp.Status,
		ContainerID: resp.ID,
		DiskName:    resp.DiskName,
		ExecURL:     fmt.Sprintf("ws://teamagentica-mc-%s:9100/exec", resp.ID),
	}, nil
}

// List returns all workspaces from workspace-manager.
func (wc *WorkspaceClient) List(ctx context.Context) ([]*Workspace, error) {
	data, err := wc.sdkClient.RouteToPlugin(ctx, "workspace-manager", "GET", "/workspaces", nil)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}

	var resp struct {
		Workspaces []createWorkspaceResponse `json:"workspaces"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode workspaces response: %w", err)
	}

	result := make([]*Workspace, len(resp.Workspaces))
	for i, w := range resp.Workspaces {
		result[i] = &Workspace{
			ID:          w.ID,
			Name:        w.Name,
			Subdomain:   w.Subdomain,
			Status:      w.Status,
			ContainerID: w.ID,
			DiskName:    w.DiskName,
			ExecURL:     fmt.Sprintf("ws://teamagentica-mc-%s:9100/exec", w.ID),
		}
	}
	return result, nil
}

// execInitMessage matches the exec-server's init frame protocol.
type execInitMessage struct {
	Model        string `json:"model"`
	SystemPrompt string `json:"system_prompt"`
	MCPConfig    string `json:"mcp_config"`
	MaxTurns     int    `json:"max_turns"`
}

// execUserMessage matches the exec-server's user message protocol.
type execUserMessage struct {
	Type           string `json:"type"`
	Prompt         string `json:"prompt"`
	ConversationID string `json:"conversation_id"`
}

// ExecOptions configures an exec session.
type ExecOptions struct {
	Model          string // Claude model to use (default: empty = exec-server default)
	SystemPrompt   string // system prompt for the session
	MaxTurns       int    // max tool-use turns (default: 5)
	ConversationID string // reuse an existing conversation
	Timeout        time.Duration // overall timeout (default: 5 minutes)
}

// Exec runs a command/prompt in the workspace via the exec-server WebSocket.
// It connects to the workspace's exec-server, sends the prompt, and collects
// the full response. The exec-server runs Claude CLI inside the workspace
// container, giving full filesystem and tool access.
func (wc *WorkspaceClient) Exec(ctx context.Context, ws *Workspace, prompt string, opts *ExecOptions) (string, error) {
	if opts == nil {
		opts = &ExecOptions{}
	}

	var result strings.Builder
	err := wc.ExecStream(ctx, ws, prompt, opts, func(event ExecEvent) {
		if event.Type == "text" || event.Type == "result" {
			result.WriteString(event.Content)
		}
	})
	if err != nil {
		return result.String(), err
	}
	return result.String(), nil
}

// ExecEvent represents a streaming event from the exec-server.
type ExecEvent struct {
	Type    string // "text", "result", "error", "tool_use", "status"
	Content string
}

// ExecStream runs a prompt in the workspace and streams output via callback.
// The callback receives ExecEvents as they arrive from the exec-server.
func (wc *WorkspaceClient) ExecStream(ctx context.Context, ws *Workspace, prompt string, opts *ExecOptions, onEvent func(ExecEvent)) error {
	if ws.ExecURL == "" {
		return fmt.Errorf("workspace %s has no exec URL", ws.ID)
	}
	if opts == nil {
		opts = &ExecOptions{}
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Connect to exec-server WebSocket.
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	conn, _, err := dialer.DialContext(ctx, ws.ExecURL, nil)
	if err != nil {
		return fmt.Errorf("connect to exec-server at %s: %w", ws.ExecURL, err)
	}
	defer conn.Close()

	// Send init frame.
	maxTurns := opts.MaxTurns
	if maxTurns == 0 {
		maxTurns = 5
	}
	initMsg := execInitMessage{
		Model:        opts.Model,
		SystemPrompt: opts.SystemPrompt,
		MaxTurns:     maxTurns,
	}
	if err := conn.WriteJSON(initMsg); err != nil {
		return fmt.Errorf("send init frame: %w", err)
	}

	// Read attached confirmation.
	var status map[string]string
	if err := conn.ReadJSON(&status); err != nil {
		return fmt.Errorf("read init response: %w", err)
	}
	if errMsg, ok := status["error"]; ok {
		return fmt.Errorf("exec-server init error: %s", errMsg)
	}
	if status["status"] != "attached" {
		return fmt.Errorf("unexpected init response: %v", status)
	}

	if onEvent != nil {
		onEvent(ExecEvent{Type: "status", Content: "attached"})
	}

	// Send user prompt.
	userMsg := execUserMessage{
		Type:           "user",
		Prompt:         prompt,
		ConversationID: opts.ConversationID,
	}
	if err := conn.WriteJSON(userMsg); err != nil {
		return fmt.Errorf("send prompt: %w", err)
	}

	// Read streamed response events.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			// Connection closed after streaming completes.
			if strings.Contains(err.Error(), "close") {
				return nil
			}
			return fmt.Errorf("read exec event: %w", err)
		}

		// Parse the Claude CLI StreamEvent.
		var event map[string]interface{}
		if json.Unmarshal(msg, &event) != nil {
			continue
		}

		execEvent := parseExecEvent(event)
		if onEvent != nil && execEvent.Type != "" {
			onEvent(execEvent)
		}

		// Check for terminal events.
		if execEvent.Type == "result" || execEvent.Type == "error" {
			return nil
		}
	}
}

// parseExecEvent converts a raw Claude CLI stream event into an ExecEvent.
// The Claude CLI emits events with a "type" field: "assistant", "result", "error", etc.
func parseExecEvent(event map[string]interface{}) ExecEvent {
	eventType, _ := event["type"].(string)

	switch eventType {
	case "assistant":
		// Text content from assistant.
		if content, ok := event["content"].(string); ok {
			return ExecEvent{Type: "text", Content: content}
		}
		// Could be structured content (content_block).
		if message, ok := event["message"].(string); ok {
			return ExecEvent{Type: "text", Content: message}
		}
	case "content_block_delta":
		if delta, ok := event["delta"].(map[string]interface{}); ok {
			if text, ok := delta["text"].(string); ok {
				return ExecEvent{Type: "text", Content: text}
			}
		}
	case "result":
		content, _ := event["result"].(string)
		if content == "" {
			if raw, err := json.Marshal(event); err == nil {
				content = string(raw)
			}
		}
		return ExecEvent{Type: "result", Content: content}
	case "error":
		errMsg, _ := event["error"].(string)
		if errMsg == "" {
			errMsg, _ = event["message"].(string)
		}
		return ExecEvent{Type: "error", Content: errMsg}
	case "tool_use":
		name, _ := event["name"].(string)
		return ExecEvent{Type: "tool_use", Content: name}
	}

	// For text streaming: some events just have a "text" field.
	if text, ok := event["text"].(string); ok && text != "" {
		return ExecEvent{Type: "text", Content: text}
	}

	return ExecEvent{}
}

// Destroy tears down a workspace via workspace-manager.
func (wc *WorkspaceClient) Destroy(ctx context.Context, ws *Workspace) error {
	_, err := wc.sdkClient.RouteToPlugin(ctx, "workspace-manager", "DELETE", "/workspaces/"+ws.ID, nil)
	if err != nil {
		return fmt.Errorf("destroy workspace %s: %w", ws.ID, err)
	}

	wc.mu.Lock()
	delete(wc.workspaces, ws.ID)
	wc.mu.Unlock()

	log.Printf("agentkit/workspace: destroyed %s (id=%s)", ws.Name, ws.ID)
	return nil
}

// DestroyAll cleans up all workspaces created by this client.
// Useful for agent shutdown cleanup.
func (wc *WorkspaceClient) DestroyAll(ctx context.Context) {
	wc.mu.Lock()
	ids := make([]string, 0, len(wc.workspaces))
	for id := range wc.workspaces {
		ids = append(ids, id)
	}
	wc.mu.Unlock()

	for _, id := range ids {
		ws := &Workspace{ID: id}
		if err := wc.Destroy(ctx, ws); err != nil {
			log.Printf("agentkit/workspace: cleanup failed for %s: %v", id, err)
		}
	}
}
