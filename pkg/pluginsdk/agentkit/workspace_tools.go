package agentkit

import (
	"context"
	"encoding/json"
	"time"
)

// WorkspaceTools returns ToolDefinitions for workspace operations that agents
// can expose to LLMs. These are handled by the agentkit runtime directly
// (not routed to an MCP plugin).
func WorkspaceTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "workspace__create",
			Description: "Create a new workspace with a specific environment for running code. Returns workspace ID and connection details.",
			Parameters: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Human-friendly name for the workspace",
					},
					"environment_id": map[string]interface{}{
						"type":        "string",
						"description": "Workspace environment plugin ID (e.g. 'workspace-env-devbox')",
					},
					"git_repo": map[string]interface{}{
						"type":        "string",
						"description": "Optional: Git repository URL to clone into the workspace",
					},
					"git_ref": map[string]interface{}{
						"type":        "string",
						"description": "Optional: Git ref to checkout after cloning (branch, tag, or commit)",
					},
				},
				"required": []string{"name", "environment_id"},
			}),
		},
		{
			Name:        "workspace__exec",
			Description: "Execute a prompt/command in an active workspace via the Claude CLI running inside it. The workspace has full filesystem and tool access.",
			Parameters: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workspace_id": map[string]interface{}{
						"type":        "string",
						"description": "The workspace ID returned from workspace__create",
					},
					"prompt": map[string]interface{}{
						"type":        "string",
						"description": "The prompt or command to execute in the workspace",
					},
					"conversation_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional: Conversation ID to continue a previous session",
					},
				},
				"required": []string{"workspace_id", "prompt"},
			}),
		},
		{
			Name:        "workspace__list",
			Description: "List all active workspaces with their status and connection details.",
			Parameters: mustJSON(map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}),
		},
		{
			Name:        "workspace__destroy",
			Description: "Destroy a workspace when done. The disk data is preserved and can be reused.",
			Parameters: mustJSON(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workspace_id": map[string]interface{}{
						"type":        "string",
						"description": "The workspace ID to destroy",
					},
				},
				"required": []string{"workspace_id"},
			}),
		},
	}
}

// WorkspaceToolHandler executes workspace tool calls. It should be checked
// before routing to MCP plugins -- workspace tools are handled at the runtime
// level, not via plugin routing.
type WorkspaceToolHandler struct {
	client *WorkspaceClient
}

// NewWorkspaceToolHandler creates a handler for workspace tool calls.
func NewWorkspaceToolHandler(client *WorkspaceClient) *WorkspaceToolHandler {
	return &WorkspaceToolHandler{client: client}
}

// IsWorkspaceTool returns true if the tool name is a workspace tool.
func IsWorkspaceTool(name string) bool {
	switch name {
	case "workspace__create", "workspace__exec", "workspace__list", "workspace__destroy":
		return true
	}
	return false
}

// Execute handles a workspace tool call and returns the result as JSON.
func (h *WorkspaceToolHandler) Execute(ctx context.Context, call ToolCall) (string, error) {
	switch call.Name {
	case "workspace__create":
		return h.handleCreate(ctx, call.Arguments)
	case "workspace__exec":
		return h.handleExec(ctx, call.Arguments)
	case "workspace__list":
		return h.handleList(ctx)
	case "workspace__destroy":
		return h.handleDestroy(ctx, call.Arguments)
	default:
		return "", nil
	}
}

func (h *WorkspaceToolHandler) handleCreate(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Name          string `json:"name"`
		EnvironmentID string `json:"environment_id"`
		GitRepo       string `json:"git_repo"`
		GitRef        string `json:"git_ref"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	ws, err := h.client.Create(ctx, WorkspaceConfig{
		Name:          params.Name,
		EnvironmentID: params.EnvironmentID,
		GitRepo:       params.GitRepo,
		GitRef:        params.GitRef,
	})
	if err != nil {
		return "", err
	}

	result, _ := json.Marshal(map[string]string{
		"workspace_id": ws.ID,
		"name":         ws.Name,
		"subdomain":    ws.Subdomain,
		"status":       ws.Status,
		"disk_name":    ws.DiskName,
	})
	return string(result), nil
}

func (h *WorkspaceToolHandler) handleExec(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		WorkspaceID    string `json:"workspace_id"`
		Prompt         string `json:"prompt"`
		ConversationID string `json:"conversation_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Look up workspace -- try local cache first, then fetch from API.
	h.client.mu.Lock()
	ws, ok := h.client.workspaces[params.WorkspaceID]
	h.client.mu.Unlock()

	if !ok {
		fetched, err := h.client.Get(ctx, params.WorkspaceID)
		if err != nil {
			return "", err
		}
		ws = fetched
	}

	result, err := h.client.Exec(ctx, ws, params.Prompt, &ExecOptions{
		ConversationID: params.ConversationID,
	})
	if err != nil {
		return "", err
	}

	resp, _ := json.Marshal(map[string]string{
		"output": result,
	})
	return string(resp), nil
}

func (h *WorkspaceToolHandler) handleList(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	workspaces, err := h.client.List(ctx)
	if err != nil {
		return "", err
	}

	type wsEntry struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Status    string `json:"status"`
		Subdomain string `json:"subdomain"`
		DiskName  string `json:"disk_name"`
	}
	entries := make([]wsEntry, len(workspaces))
	for i, ws := range workspaces {
		entries[i] = wsEntry{
			ID:        ws.ID,
			Name:      ws.Name,
			Status:    ws.Status,
			Subdomain: ws.Subdomain,
			DiskName:  ws.DiskName,
		}
	}

	result, _ := json.Marshal(map[string]interface{}{
		"workspaces": entries,
		"count":      len(entries),
	})
	return string(result), nil
}

func (h *WorkspaceToolHandler) handleDestroy(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	ws := &Workspace{ID: params.WorkspaceID}
	if err := h.client.Destroy(ctx, ws); err != nil {
		return "", err
	}

	result, _ := json.Marshal(map[string]string{
		"status":       "destroyed",
		"workspace_id": params.WorkspaceID,
	})
	return string(result), nil
}

// mustJSON marshals v to json.RawMessage. Panics on error (only used for static schemas).
func mustJSON(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic("mustJSON: " + err.Error())
	}
	return b
}
