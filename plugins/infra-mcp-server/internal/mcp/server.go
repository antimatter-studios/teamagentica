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
	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
)

// Server wraps mcp-go's MCPServer with our tool discovery and routing logic.
type Server struct {
	sdk      *pluginsdk.Client
	aliases  *alias.AliasMap
	debug    bool
	mcpSrv   *mcpsrv.MCPServer
	httpSrv  *mcpsrv.StreamableHTTPServer
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

	// Create mcp-go server with tool capabilities (listChanged=true).
	s.mcpSrv = mcpsrv.NewMCPServer(
		"teamagentica-mcp-server",
		"1.0.0",
		mcpsrv.WithToolCapabilities(true),
	)

	// Register builtin meta-tools.
	s.registerBuiltinTools()

	// Discover and register plugin tools.
	s.RefreshTools()

	// Create the streamable HTTP transport.
	s.httpSrv = mcpsrv.NewStreamableHTTPServer(s.mcpSrv)

	return s
}

// HTTPServer returns the streamable HTTP handler for mounting on a router.
func (s *Server) HTTPServer() *mcpsrv.StreamableHTTPServer {
	return s.httpSrv
}

// UpdateAliases replaces the alias map and re-registers tools.
func (s *Server) UpdateAliases(infos []alias.AliasInfo) {
	s.aliases.Replace(infos)
	InvalidateToolCache()
	s.RefreshTools()
}

// Aliases returns the current alias map.
func (s *Server) Aliases() *alias.AliasMap {
	return s.aliases
}

// RefreshTools re-discovers plugin tools and syncs them into the mcp-go server.
func (s *Server) RefreshTools() {
	tools := BuildToolList(s.aliases)

	// Build the new tool set: builtin + discovered.
	var serverTools []mcpsrv.ServerTool

	for _, t := range tools {
		dt := t // capture for closure
		// Build Tool struct directly — using NewTool + WithRawInputSchema
		// causes a conflict (both InputSchema and RawInputSchema set).
		tool := mcplib.Tool{
			Name:           dt.FullName,
			Description:    dt.Desc,
			RawInputSchema: dt.inputSchema(),
		}
		serverTools = append(serverTools, mcpsrv.ServerTool{
			Tool:    tool,
			Handler: s.makeToolHandler(dt),
		})
	}

	// SetTools replaces all non-builtin tools atomically.
	// We re-add builtins too since SetTools replaces everything.
	serverTools = append(serverTools, s.builtinServerTools()...)
	s.mcpSrv.SetTools(serverTools...)

	if s.debug {
		log.Printf("mcp-server: registered %d tools with mcp-go", len(serverTools))
	}
}

// makeToolHandler creates a mcp-go ToolHandlerFunc that routes to the correct plugin.
func (s *Server) makeToolHandler(dt registeredTool) mcpsrv.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		if s.debug {
			log.Printf("mcp-server: tools/call name=%s", req.Params.Name)
		}

		args := req.GetRawArguments()
		argsJSON, _ := json.Marshal(args)

		// Agent chat tools need a properly formatted chat request body.
		var body *bytes.Reader
		if dt.Name == "chat" && dt.AliasName != "" {
			body = s.buildChatBody(dt, argsJSON)
		} else if len(argsJSON) > 0 {
			body = bytes.NewReader(argsJSON)
		} else {
			body = bytes.NewReader([]byte("{}"))
		}

		callCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()

		resp, err := s.sdk.RouteToPlugin(callCtx, dt.PluginID, "POST", dt.Endpoint, body)
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("Error executing %s: %v", dt.FullName, err)), nil
		}

		// Parse result for media handling.
		return s.parseToolResult(string(resp)), nil
	}
}

// buildChatBody constructs a chat request for agent delegation tools.
func (s *Server) buildChatBody(dt registeredTool, argsJSON []byte) *bytes.Reader {
	var args struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return bytes.NewReader(argsJSON)
	}

	identity := fmt.Sprintf("You are @%s (%s", dt.AliasName, dt.PluginID)
	if dt.AliasModel != "" {
		identity += ", model: " + dt.AliasModel
	}
	identity += "). You are one of several AI agents in a collaborative platform."

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
	if dt.AliasModel != "" {
		chatReq["model"] = dt.AliasModel
	}
	reqBody, _ := json.Marshal(chatReq)
	return bytes.NewReader(reqBody)
}

// parseToolResult inspects a plugin tool's JSON response. If it contains
// image_data, the binary is stored to sss3-storage and a {{media:key}} marker
// is returned as text. Video URLs become {{media_url:...}} markers.
func (s *Server) parseToolResult(result string) *mcplib.CallToolResult {
	var resp struct {
		Status    string `json:"status"`
		ImageData string `json:"image_data"`
		MimeType  string `json:"mime_type"`
		Text      string `json:"text"`
		Model     string `json:"model"`
		Prompt    string `json:"prompt"`
		VideoURL  string `json:"video_url"`
	}
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		return mcplib.NewToolResultText(result)
	}

	// Handle image data — store to sss3, return reference marker.
	if resp.ImageData != "" {
		data, err := base64.StdEncoding.DecodeString(resp.ImageData)
		if err != nil {
			log.Printf("mcp-server: failed to decode base64 image: %v", err)
			return mcplib.NewToolResultText(result)
		}

		key, err := s.storeMedia(data, resp.MimeType)
		if err != nil {
			log.Printf("mcp-server: failed to store media: %v", err)
			return mcplib.NewToolResultText(fmt.Sprintf("Image generated (model=%s) but storage failed: %v", resp.Model, err))
		}

		summary := fmt.Sprintf("Image generated (model=%s). {{media:%s}}", resp.Model, key)
		if resp.Text != "" {
			summary = resp.Text + "\n" + summary
		}
		return mcplib.NewToolResultText(summary)
	}

	// Handle external video URL — return reference marker.
	if resp.VideoURL != "" {
		summary := fmt.Sprintf("Video generated (model=%s). {{media_url:%s}}", resp.Model, resp.VideoURL)
		if resp.Text != "" {
			summary = resp.Text + "\n" + summary
		}
		return mcplib.NewToolResultText(summary)
	}

	return mcplib.NewToolResultText(result)
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

// registerBuiltinTools adds the platform meta-tools to the mcp-go server.
func (s *Server) registerBuiltinTools() {
	for _, st := range s.builtinServerTools() {
		s.mcpSrv.AddTool(st.Tool, st.Handler)
	}
}

// builtinServerTools returns builtin tools as mcp-go ServerTool entries.
func (s *Server) builtinServerTools() []mcpsrv.ServerTool {
	return []mcpsrv.ServerTool{
		{
			Tool: mcplib.NewTool("list_agents",
				mcplib.WithDescription("List all available AI agent plugins and their status"),
			),
			Handler: s.handleListAgents,
		},
		{
			Tool: mcplib.NewTool("list_tools",
				mcplib.WithDescription("List all available tool plugins and their capabilities"),
			),
			Handler: s.handleListTools,
		},
		{
			Tool: mcplib.NewTool("send_message",
				mcplib.WithDescription("Send a message to another AI agent plugin for processing. Use this to delegate tasks to specialized agents."),
				mcplib.WithString("agent_id", mcplib.Required(), mcplib.Description("The plugin ID of the agent to send the message to (e.g. agent-google, agent-moonshot)")),
				mcplib.WithString("message", mcplib.Required(), mcplib.Description("The message to send to the agent")),
				mcplib.WithString("model", mcplib.Description("Optional: specific model to use on the target agent")),
			),
			Handler: s.handleSendMessage,
		},
	}
}

func (s *Server) handleListAgents(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	agents, err := s.sdk.SearchPlugins("agent:chat")
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("Error discovering agents: %v", err)), nil
	}
	data, _ := json.MarshalIndent(agents, "", "  ")
	return mcplib.NewToolResultText(string(data)), nil
}

func (s *Server) handleListTools(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	// Refresh tools into mcp-go registry so tools/list stays in sync.
	InvalidateToolCache()
	s.RefreshTools()

	tools := BuildToolList(s.aliases)
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
	return mcplib.NewToolResultText(string(data)), nil
}

func (s *Server) handleSendMessage(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	agentID := req.GetString("agent_id", "")
	message := req.GetString("message", "")
	model := req.GetString("model", "")

	if agentID == "" || message == "" {
		return mcplib.NewToolResultError("agent_id and message are required"), nil
	}

	// Resolve alias to actual plugin ID.
	pluginID, aliasModel := s.resolveAlias(agentID)
	if s.debug {
		log.Printf("mcp-server: send_message agent_id=%s resolved to plugin=%s model=%s", agentID, pluginID, aliasModel)
	}

	identity := fmt.Sprintf("You are @%s (%s). You are one of several AI agents in a collaborative platform.", agentID, pluginID)
	if model == "" {
		model = aliasModel
	}

	callCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	done, err := s.sdk.AgentChat(callCtx, pluginID, pluginsdk.AgentChatRequest{
		Message: message,
		Model:   model,
		Conversation: []pluginsdk.ConversationMsg{
			{Role: "system", Content: identity},
			{Role: "user", Content: message},
		},
	})
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("Agent %s error: %v", agentID, err)), nil
	}

	return mcplib.NewToolResultText(done.Response), nil
}

// resolveAlias resolves an alias name to a plugin ID and optional model.
func (s *Server) resolveAlias(name string) (pluginID, model string) {
	target := s.aliases.Resolve(name)
	if target != nil {
		return target.PluginID, target.Model
	}
	return name, ""
}
