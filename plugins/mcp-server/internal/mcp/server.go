package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
)

// Server implements the MCP protocol logic.
type Server struct {
	sdk     *pluginsdk.Client
	aliases *alias.AliasMap
	debug   bool
}

// NewServer creates an MCP server backed by the plugin SDK for tool routing.
func NewServer(sdk *pluginsdk.Client, debug bool) *Server {
	s := &Server{sdk: sdk, debug: debug}
	// Seed aliases from kernel.
	infos, err := sdk.FetchAliases()
	if err != nil {
		log.Printf("mcp-server: failed to fetch aliases: %v", err)
		s.aliases = alias.NewAliasMap(nil)
	} else {
		s.aliases = alias.NewAliasMap(infos)
		log.Printf("mcp-server: loaded %d aliases", len(infos))
	}
	return s
}

// UpdateAliases replaces the alias map with new entries (for live event updates).
func (s *Server) UpdateAliases(infos []alias.AliasInfo) {
	s.aliases.Replace(infos)
	InvalidateCache() // Force tool re-discovery with new alias names.
}

// Aliases returns the current alias map (for use by handlers).
func (s *Server) Aliases() *alias.AliasMap {
	return s.aliases
}

// resolveAlias resolves an alias name to a plugin ID and optional model.
// Returns the original name unchanged if it's not an alias.
func (s *Server) resolveAlias(name string) (pluginID, model string) {
	target := s.aliases.Resolve(name)
	if target != nil {
		return target.PluginID, target.Model
	}
	return name, ""
}

// HandleMessage processes a single MCP JSON-RPC request and returns a response.
func (s *Server) HandleMessage(raw []byte) *Response {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   &RPCError{Code: -32700, Message: "Parse error"},
		}
	}

	if req.JSONRPC != "2.0" {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32600, Message: "Invalid Request: jsonrpc must be 2.0"},
		}
	}

	if s.debug {
		log.Printf("mcp-server: method=%s", req.Method)
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "notifications/initialized":
		// Client ack — no response needed for notifications.
		return nil
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	case "ping":
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{}}
	default:
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)},
		}
	}
}

func (s *Server) handleInitialize(req Request) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: InitializeResult{
			ProtocolVersion: "2025-03-26",
			ServerInfo: ServerInfo{
				Name:    "teamagentica-mcp-server",
				Version: "1.0.0",
			},
			Capabilities: Capabilities{
				Tools: &ToolsCapability{ListChanged: true},
			},
		},
	}
}

func (s *Server) handleToolsList(req Request) *Response {
	tools := DiscoverTools(s.sdk, s.aliases)

	// Build MCP tool definitions from discovered plugin tools.
	mcpTools := make([]ToolDef, 0, len(tools)+3)

	// Add platform meta-tools.
	mcpTools = append(mcpTools, s.builtinTools()...)

	// Add plugin tools.
	for _, t := range tools {
		schema := t.Parameters
		if schema == nil {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		mcpTools = append(mcpTools, ToolDef{
			Name:        t.FullName,
			Description: t.Desc,
			InputSchema: schema,
		})
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolsListResult{Tools: mcpTools},
	}
}

func (s *Server) handleToolsCall(req Request) *Response {
	var params ToolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32602, Message: "Invalid params: " + err.Error()},
		}
	}

	if s.debug {
		log.Printf("mcp-server: tools/call name=%s args=%s", params.Name, string(params.Arguments))
	}

	// Handle builtin tools.
	switch params.Name {
	case "list_agents":
		return s.callListAgents(req)
	case "list_tools":
		return s.callListTools(req)
	case "send_message":
		return s.callSendMessage(req, params)
	}

	// Route to plugin tool.
	result, err := s.executePluginTool(params)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolsCallResult{
				Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
				IsError: true,
			},
		}
	}

	// Check if the response contains media and store to sss3-storage, returning references.
	content := s.parseToolResult(result)

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolsCallResult{Content: content},
	}
}

// parseToolResult inspects a plugin tool's JSON response. If it contains
// image_data, the binary is stored to sss3-storage and a {{media:key}} marker
// is returned as text (no raw base64 in the LLM context). Video URLs become
// {{media_url:...}} markers. Otherwise the raw JSON is returned as text.
func (s *Server) parseToolResult(result string) []ContentBlock {
	var resp struct {
		Status   string `json:"status"`
		ImageData string `json:"image_data"`
		MimeType  string `json:"mime_type"`
		Text      string `json:"text"`
		Model     string `json:"model"`
		Prompt    string `json:"prompt"`
		VideoURL  string `json:"video_url"`
	}
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		return []ContentBlock{{Type: "text", Text: result}}
	}

	// Handle image data — store to sss3, return reference marker.
	if resp.ImageData != "" {
		data, err := base64.StdEncoding.DecodeString(resp.ImageData)
		if err != nil {
			log.Printf("mcp-server: failed to decode base64 image: %v", err)
			return []ContentBlock{{Type: "text", Text: result}}
		}

		key, err := s.storeMedia(data, resp.MimeType)
		if err != nil {
			log.Printf("mcp-server: failed to store media: %v", err)
			// Fallback: return text-only summary without the huge base64.
			return []ContentBlock{{Type: "text", Text: fmt.Sprintf("Image generated (model=%s) but storage failed: %v", resp.Model, err)}}
		}

		summary := fmt.Sprintf("Image generated (model=%s). {{media:%s}}", resp.Model, key)
		if resp.Text != "" {
			summary = resp.Text + "\n" + summary
		}
		return []ContentBlock{{Type: "text", Text: summary}}
	}

	// Handle external video URL — return reference marker.
	if resp.VideoURL != "" {
		summary := fmt.Sprintf("Video generated (model=%s). {{media_url:%s}}", resp.Model, resp.VideoURL)
		if resp.Text != "" {
			summary = resp.Text + "\n" + summary
		}
		return []ContentBlock{{Type: "text", Text: summary}}
	}

	return []ContentBlock{{Type: "text", Text: result}}
}

// storeMedia writes binary data to sss3-storage under media/generated/{uuid}.{ext}.
func (s *Server) storeMedia(data []byte, mimeType string) (string, error) {
	ext := ".bin"
	switch mimeType {
	case "image/png":
		ext = ".png"
	case "image/jpeg":
		ext = ".jpg"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	case "video/mp4":
		ext = ".mp4"
	}

	key := "media/generated/" + uuid.New().String() + ext
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.sdk.StorageWrite(ctx, key, bytes.NewReader(data), mimeType); err != nil {
		return "", fmt.Errorf("StorageWrite %s: %w", key, err)
	}

	if s.debug {
		log.Printf("mcp-server: stored media %s (%d bytes, %s)", key, len(data), mimeType)
	}
	return key, nil
}

func (s *Server) executePluginTool(params ToolsCallParams) (string, error) {
	tools := DiscoverTools(s.sdk, s.aliases)
	var matched *discoveredTool
	for i, t := range tools {
		if t.FullName == params.Name {
			matched = &tools[i]
			break
		}
	}
	if matched == nil {
		return "", fmt.Errorf("tool %s not found", params.Name)
	}

	pluginID := matched.PluginID
	endpoint := matched.Endpoint

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var body *bytes.Reader

	// Agent chat tools need a properly formatted chat request body.
	if matched.Name == "chat" && matched.AliasName != "" {
		var args struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			return "", fmt.Errorf("invalid arguments for %s: %w", params.Name, err)
		}

		// Build identity system prompt so the agent knows who it is.
		identity := fmt.Sprintf("You are @%s (%s", matched.AliasName, matched.PluginID)
		if matched.AliasModel != "" {
			identity += ", model: " + matched.AliasModel
		}
		identity += "). You are one of several AI agents in a collaborative platform."

		// List other available agents/tools for context.
		if block := s.aliases.SystemPromptBlock(); block != "" {
			identity += "\n\n" + block
		}

		chatReq := map[string]interface{}{
			"message": args.Message,
			"conversation": []map[string]string{
				{"role": "system", "content": identity},
				{"role": "user", "content": args.Message},
			},
		}
		if matched.AliasModel != "" {
			chatReq["model"] = matched.AliasModel
		}
		reqBody, _ := json.Marshal(chatReq)
		body = bytes.NewReader(reqBody)
	} else if params.Arguments != nil && len(params.Arguments) > 0 {
		body = bytes.NewReader(params.Arguments)
	} else {
		body = bytes.NewReader([]byte("{}"))
	}

	resp, err := s.sdk.RouteToPlugin(ctx, pluginID, "POST", endpoint, body)
	if err != nil {
		return "", fmt.Errorf("execute tool %s: %w", params.Name, err)
	}

	return string(resp), nil
}

// builtinTools returns the platform meta-tool definitions.
func (s *Server) builtinTools() []ToolDef {
	return []ToolDef{
		{
			Name:        "list_agents",
			Description: "List all available AI agent plugins and their status",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "list_tools",
			Description: "List all available tool plugins and their capabilities",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "send_message",
			Description: "Send a message to another AI agent plugin for processing. Use this to delegate tasks to specialized agents.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"agent_id":{"type":"string","description":"The plugin ID of the agent to send the message to (e.g. agent-gemini, agent-kimi)"},"message":{"type":"string","description":"The message to send to the agent"},"model":{"type":"string","description":"Optional: specific model to use on the target agent"}},"required":["agent_id","message"]}`),
		},
	}
}

func (s *Server) callListAgents(req Request) *Response {
	agents, err := s.sdk.SearchPlugins("ai:chat")
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolsCallResult{
				Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Error discovering agents: %v", err)}},
				IsError: true,
			},
		}
	}

	data, _ := json.MarshalIndent(agents, "", "  ")
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: ToolsCallResult{
			Content: []ContentBlock{{Type: "text", Text: string(data)}},
		},
	}
}

func (s *Server) callListTools(req Request) *Response {
	tools := DiscoverTools(s.sdk, s.aliases)

	type toolInfo struct {
		Name        string `json:"name"`
		PluginID    string `json:"plugin_id"`
		Description string `json:"description"`
	}
	infos := make([]toolInfo, len(tools))
	for i, t := range tools {
		infos[i] = toolInfo{Name: t.FullName, PluginID: t.PluginID, Description: t.Desc}
	}

	data, _ := json.MarshalIndent(infos, "", "  ")
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: ToolsCallResult{
			Content: []ContentBlock{{Type: "text", Text: string(data)}},
		},
	}
}

func (s *Server) callSendMessage(req Request, params ToolsCallParams) *Response {
	var args struct {
		AgentID string `json:"agent_id"`
		Message string `json:"message"`
		Model   string `json:"model"`
	}
	if err := json.Unmarshal(params.Arguments, &args); err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolsCallResult{
				Content: []ContentBlock{{Type: "text", Text: "Invalid arguments: " + err.Error()}},
				IsError: true,
			},
		}
	}

	if args.AgentID == "" || args.Message == "" {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolsCallResult{
				Content: []ContentBlock{{Type: "text", Text: "agent_id and message are required"}},
				IsError: true,
			},
		}
	}

	// Resolve alias to actual plugin ID (e.g. "nb2" → "tool-nanobanana").
	pluginID, aliasModel := s.resolveAlias(args.AgentID)
	if s.debug {
		log.Printf("mcp-server: send_message agent_id=%s resolved to plugin=%s model=%s", args.AgentID, pluginID, aliasModel)
	}

	// Build a chat request matching agent expectations: {message, conversation, model}.
	// Build identity system prompt so the agent knows who it is.
	identity := fmt.Sprintf("You are @%s (%s). You are one of several AI agents in a collaborative platform.", args.AgentID, pluginID)
	chatReq := map[string]interface{}{
		"message": args.Message,
		"conversation": []map[string]string{
			{"role": "system", "content": identity},
			{"role": "user", "content": args.Message},
		},
	}
	model := args.Model
	if model == "" {
		model = aliasModel
	}
	if model != "" {
		chatReq["model"] = model
	}
	body, _ := json.Marshal(chatReq)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	resp, err := s.sdk.RouteToPlugin(ctx, pluginID, "POST", "/chat", bytes.NewReader(body))
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolsCallResult{
				Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Error routing to %s (plugin=%s): %v", args.AgentID, pluginID, err)}},
				IsError: true,
			},
		}
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: ToolsCallResult{
			Content: []ContentBlock{{Type: "text", Text: string(resp)}},
		},
	}
}
