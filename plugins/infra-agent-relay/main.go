package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-relay/internal/bridge"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-relay/internal/debugtrace"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-relay/internal/router"
	"github.com/gin-gonic/gin"
)

// relay is the central message routing service.
// Messaging plugins send messages here; the relay resolves the target agent
// (via @mention or the default persona) and streams the response back.
type relay struct {
	mu                    sync.RWMutex
	conns                 map[string]*bridge.Client // workspaceID → TCP connection
	routes                *router.Table
	sdk                   *pluginsdk.Client
	taskTimeoutSeconds    int
	debug                 bool
	tracer                *debugtrace.Recorder
	personas              *personaCache
	memoryPluginID        string // cached plugin ID for infra-agent-memory (empty = not available)
	memoryMu              sync.RWMutex
	memoryCheckedAt       time.Time

	// Last active session for progress forwarding (single-request assumption).
	// TODO: Replace with correlation ID scheme for multi-user support.
	lastSessionMu     sync.RWMutex
	lastSourcePlugin  string
	lastChannelID     string

	// Pending async tasks — maps plugin task ID → waiter info.
	// Used to wait for async plugin completion (e.g. video generation).
	asyncMu      sync.Mutex
	asyncWaiters map[string]*asyncWaiter
}

// personaInfo holds a cached persona definition from infra-alias-registry.
type personaInfo struct {
	Alias        string `json:"alias"`
	SystemPrompt string `json:"system_prompt"`
	BackendAlias string `json:"backend_alias"`
	Model        string `json:"model"`
	Role         string `json:"role"`
	IsDefault    *bool  `json:"is_default"`
}

// personaCache caches all persona definitions with a TTL.
type personaCache struct {
	mu          sync.RWMutex
	pluginID    string
	personas    map[string]personaInfo
	fetchedAt   time.Time
	ttl         time.Duration
}

func newRelay(sdk *pluginsdk.Client) *relay {
	return &relay{
		conns:                 make(map[string]*bridge.Client),
		routes:                router.NewTable(),
		sdk:                   sdk,
		taskTimeoutSeconds:    120,
		personas:              &personaCache{ttl: 60 * time.Second},
		asyncWaiters:          make(map[string]*asyncWaiter),
	}
}

// asyncWaiter tracks a pending async task with its session context so that
// incoming progress events can be forwarded with the correct task_group_id.
type asyncWaiter struct {
	ch           chan *agentChatResponse
	taskGroupID  string
	sourcePlugin string
	channelID    string
}

// registerAsyncWaiter creates a channel that will receive the result when an
// async plugin completes. Returns the channel. Caller must call removeAsyncWaiter.
func (r *relay) registerAsyncWaiter(taskID, taskGroupID, sourcePlugin, channelID string) chan *agentChatResponse {
	w := &asyncWaiter{
		ch:           make(chan *agentChatResponse, 1),
		taskGroupID:  taskGroupID,
		sourcePlugin: sourcePlugin,
		channelID:    channelID,
	}
	r.asyncMu.Lock()
	r.asyncWaiters[taskID] = w
	r.asyncMu.Unlock()
	return w.ch
}

func (r *relay) removeAsyncWaiter(taskID string) {
	r.asyncMu.Lock()
	delete(r.asyncWaiters, taskID)
	r.asyncMu.Unlock()
}

// lookupAsyncWaiter returns the session context for a pending async task.
func (r *relay) lookupAsyncWaiter(taskID string) (taskGroupID, sourcePlugin, channelID string, ok bool) {
	r.asyncMu.Lock()
	w, exists := r.asyncWaiters[taskID]
	r.asyncMu.Unlock()
	if !exists {
		return "", "", "", false
	}
	return w.taskGroupID, w.sourcePlugin, w.channelID, true
}

// resolveAsyncWaiter delivers a result to a waiting async task.
// Returns true if a waiter was found and notified.
func (r *relay) resolveAsyncWaiter(taskID string, resp *agentChatResponse) bool {
	r.asyncMu.Lock()
	w, ok := r.asyncWaiters[taskID]
	r.asyncMu.Unlock()
	if ok {
		w.ch <- resp
		return true
	}
	return false
}

// fetchPersonas returns all personas from infra-alias-registry, using a TTL cache.
// Returns an empty map if the plugin is not installed or unavailable.
func (r *relay) fetchPersonas() map[string]personaInfo {
	cache := r.personas

	cache.mu.RLock()
	if time.Since(cache.fetchedAt) < cache.ttl && cache.personas != nil {
		p := cache.personas
		cache.mu.RUnlock()
		return p
	}
	cache.mu.RUnlock()

	cache.mu.Lock()
	defer cache.mu.Unlock()

	// Double-check after acquiring write lock.
	if time.Since(cache.fetchedAt) < cache.ttl && cache.personas != nil {
		return cache.personas
	}

	empty := map[string]personaInfo{}

	plugins, err := r.sdk.SearchPlugins("tool:personas")
	if err != nil || len(plugins) == 0 {
		cache.personas = empty
		cache.fetchedAt = time.Now()
		return empty
	}

	pluginID := plugins[0].ID
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	body, err := r.sdk.RouteToPlugin(ctx, pluginID, "GET", "/personas", nil)
	if err != nil {
		log.Printf("relay: persona fetch from %s failed: %v", pluginID, err)
		cache.personas = empty
		cache.fetchedAt = time.Now()
		return empty
	}

	var resp struct {
		Personas []personaInfo `json:"personas"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		cache.personas = empty
		cache.fetchedAt = time.Now()
		return empty
	}

	personas := make(map[string]personaInfo, len(resp.Personas))
	for _, p := range resp.Personas {
		personas[p.Alias] = p
	}
	cache.personas = personas
	cache.fetchedAt = time.Now()

	if len(personas) > 0 {
		log.Printf("relay: loaded %d personas from %s", len(personas), pluginID)
	}
	return personas
}

// lookupPersona returns the persona for agentAlias, or nil if not found.
func (r *relay) lookupPersona(agentAlias string) *personaInfo {
	if agentAlias == "" || r.sdk == nil {
		return nil
	}
	personas := r.fetchPersonas()
	if p, ok := personas[agentAlias]; ok {
		return &p
	}
	return nil
}

// resolvedTarget holds the result of persona-first, alias-fallback resolution.
type resolvedTarget struct {
	PluginID     string
	Model        string
	SystemPrompt string // from persona; empty for raw alias calls
	Alias        string // the name that was resolved
}

// resolvePersona looks up a name as a persona and resolves its backend alias
// to a concrete plugin + model. Returns nil if no persona exists for the name.
// Bare aliases without personas are not chattable — they are infrastructure-only.
func (r *relay) resolvePersona(name string, aliases *alias.AliasMap) *resolvedTarget {
	if p := r.lookupPersona(name); p != nil && p.BackendAlias != "" {
		target := aliases.Resolve(p.BackendAlias)
		if target != nil {
			model := p.Model
			if model == "" {
				model = target.Model
			}
			return &resolvedTarget{
				PluginID:     target.PluginID,
				Model:        model,
				SystemPrompt: p.SystemPrompt,
				Alias:        name,
			}
		}
	}
	return nil
}

// resolveAlias looks up a bare alias for non-chat routing (e.g. tool calls).
func (r *relay) resolveAlias(name string, aliases *alias.AliasMap) *resolvedTarget {
	if aliases == nil {
		return nil
	}
	target := aliases.Resolve(name)
	if target == nil {
		return nil
	}
	return &resolvedTarget{
		PluginID: target.PluginID,
		Model:    target.Model,
		Alias:    name,
	}
}

// parseAtPrefix extracts an @name prefix from a message.
// Returns (name, remainder, true) if found, ("", "", false) otherwise.
func parseAtPrefix(text string) (string, string, bool) {
	if !strings.HasPrefix(text, "@") {
		return "", "", false
	}
	rest := text[1:]
	spaceIdx := strings.IndexAny(rest, " \t\n")
	if spaceIdx < 0 {
		return strings.ToLower(rest), "", true
	}
	return strings.ToLower(rest[:spaceIdx]), strings.TrimSpace(rest[spaceIdx+1:]), true
}

// --- Memory integration ---

// memoryPlugin returns the plugin ID of the infra-agent-memory plugin, or "" if unavailable.
// Result is cached for 60 seconds to avoid repeated discovery calls.
func (r *relay) memoryPlugin() string {
	r.memoryMu.RLock()
	if time.Since(r.memoryCheckedAt) < 60*time.Second {
		id := r.memoryPluginID
		r.memoryMu.RUnlock()
		return id
	}
	r.memoryMu.RUnlock()

	r.memoryMu.Lock()
	defer r.memoryMu.Unlock()
	if time.Since(r.memoryCheckedAt) < 60*time.Second {
		return r.memoryPluginID
	}

	plugins, err := r.sdk.SearchPlugins("tool:memory")
	if err != nil || len(plugins) == 0 {
		r.memoryPluginID = ""
	} else {
		r.memoryPluginID = plugins[0].ID
	}
	r.memoryCheckedAt = time.Now()
	return r.memoryPluginID
}

// memoryGetHistory fetches conversation history for a session from infra-agent-memory.
// Returns nil if the memory plugin is unavailable.
func (r *relay) memoryGetHistory(ctx context.Context, sessionID string) []conversationMsg {
	pluginID := r.memoryPlugin()
	if pluginID == "" {
		return nil
	}

	body, err := r.sdk.RouteToPlugin(ctx, pluginID, "GET",
		"/sessions/"+sessionID+"/messages", nil)
	if err != nil {
		log.Printf("relay: memory fetch history failed: %v", err)
		return nil
	}

	var resp struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}

	msgs := make([]conversationMsg, len(resp.Messages))
	for i, m := range resp.Messages {
		msgs[i] = conversationMsg{Role: m.Role, Content: m.Content}
	}
	return msgs
}

// memoryStore appends a message to a session in infra-agent-memory.
// Fire-and-forget: errors are logged but not returned.
func (r *relay) memoryStore(sessionID, role, content, responder string) {
	pluginID := r.memoryPlugin()
	if pluginID == "" {
		return
	}

	payload, _ := json.Marshal(map[string]string{
		"role":      role,
		"content":   content,
		"responder": responder,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := r.sdk.RouteToPlugin(ctx, pluginID, "POST",
		"/sessions/"+sessionID+"/messages", bytes.NewReader(payload)); err != nil {
		log.Printf("relay: memory store failed: %v", err)
	}
}

// resolveDefault finds the persona marked as is_default and
// resolves its backend alias to a concrete plugin + model.
func (r *relay) resolveDefault(aliases *alias.AliasMap) *resolvedTarget {
	personas := r.fetchPersonas()
	for _, p := range personas {
		if p.IsDefault != nil && *p.IsDefault {
			return r.resolvePersona(p.Alias, aliases)
		}
	}
	return nil
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
	Response    string            `json:"response"`              // the response text/content
	Responder   string            `json:"responder,omitempty"`   // alias or plugin ID that responded
	Model       string            `json:"model,omitempty"`       // model that generated the response
	Backend     string            `json:"backend,omitempty"`     // agent backend (e.g. "api_key", "cli")
	Usage       *agentUsage       `json:"usage,omitempty"`       // token usage from the agent
	CostUSD     float64           `json:"cost_usd,omitempty"`    // cost in USD (if reported)
	DurationMs  int64             `json:"duration_ms,omitempty"` // end-to-end processing time in ms
	Attachments []agentAttachment `json:"attachments,omitempty"` // media attachments from the agent
}

// emitProgress sends a progress event to the source messaging plugin via addressed event.
func (r *relay) emitProgress(sourcePlugin, channelID, taskGroupID, status, message string, response *relayResponse) {
	payload := map[string]interface{}{
		"task_group_id": taskGroupID,
		"channel_id":    channelID,
		"status":        status,
		"message":       message,
	}
	if response != nil {
		payload["response"] = response.Response
		payload["responder"] = response.Responder
		payload["model"] = response.Model
		payload["backend"] = response.Backend
		payload["attachments"] = response.Attachments
		if response.Usage != nil {
			payload["usage"] = response.Usage
		}
		if response.CostUSD > 0 {
			payload["cost_usd"] = response.CostUSD
		}
		if response.DurationMs > 0 {
			payload["duration_ms"] = response.DurationMs
		}
	}
	// Normalize @@alias → @alias — LLMs sometimes echo the @ prefix from the
	// system prompt into alias fields, causing double-@ in progress messages.
	if message != "" {
		message = strings.ReplaceAll(message, "@@", "@")
	}
	payload["message"] = message

	data, _ := json.Marshal(payload)
	r.sdk.ReportAddressedEvent("relay:progress", string(data), sourcePlugin)
}

// handleChat is the single entry point for all messages from messaging plugins.
// Returns a task_group_id immediately; all results delivered via relay:progress events.
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
	// Workspace routing is synchronous — it has its own TCP protocol.
	if ws := r.routes.GetWorkspace(req.SourcePlugin, req.ChannelID); ws != nil {
		r.routeToWorkspace(c, ws, req)
		return
	}

	// Generate task group ID and return immediately.
	taskGroupID := "tg-" + uuid.New().String()[:8]

	// Track session for progress forwarding from external plugins.
	r.lastSessionMu.Lock()
	r.lastSourcePlugin = req.SourcePlugin
	r.lastChannelID = req.ChannelID
	r.lastSessionMu.Unlock()

	c.JSON(http.StatusAccepted, gin.H{"task_group_id": taskGroupID})

	// Process in background — all results delivered via events.
	go r.processChat(req, taskGroupID)
}

// processChat runs the actual chat processing in the background.
func (r *relay) processChat(req relayRequest, taskGroupID string) {
	processStart := time.Now()
	ctx := context.Background()
	sessionID := req.SourcePlugin + ":" + req.ChannelID

	// Emit "thinking" status immediately — include agent name if @mentioned.
	thinkingMsg := "Thinking..."
	if name, _, ok := parseAtPrefix(req.Message); ok && name != "" {
		thinkingMsg = fmt.Sprintf("@%s is thinking...", name)
	}
	r.emitProgress(req.SourcePlugin, req.ChannelID, taskGroupID, "thinking", thinkingMsg, nil)

	// Fetch conversation history from memory plugin (if available).
	history := r.memoryGetHistory(ctx, sessionID)

	// Store the incoming user message.
	go r.memoryStore(sessionID, "user", req.Message, "")

	var reqTraceID string
	if r.debug {
		reqTraceID = debugtrace.NewRequestID()
		r.tracer.Record(reqTraceID, "", debugtrace.TypeRequest, "", req.SourcePlugin, "",
			fmt.Sprintf("source=%s channel=%s message=%s", req.SourcePlugin, req.ChannelID, req.Message),
			nil, "", 0, "")
	}

	// 2. Check for @name prefix — persona direct routing.
	aliases := r.routes.Aliases()
	if name, remainder, ok := parseAtPrefix(req.Message); ok {
		resolved := r.resolvePersona(name, aliases)
		if resolved == nil {
			// @prefix was used but no persona exists — tell the user.
			rr := &relayResponse{
				Response:  fmt.Sprintf("@%s has no persona — create a persona to enable chat", name),
				Responder: "system",
			}
			r.emitProgress(req.SourcePlugin, req.ChannelID, taskGroupID, "completed", "", rr)
			return
		}
		if remainder == "" {
			rr := &relayResponse{
				Response:  fmt.Sprintf("Usage: @%s <message>", resolved.Alias),
				Responder: resolved.Alias,
			}
			r.emitProgress(req.SourcePlugin, req.ChannelID, taskGroupID, "completed", "", rr)
			return
		}

		r.emitProgress(req.SourcePlugin, req.ChannelID, taskGroupID, "running",
			fmt.Sprintf("Asking @%s...", resolved.Alias), nil)

		callStart := time.Now()
		enrichedPrompt := r.enrichSystemPrompt(resolved.SystemPrompt, aliases)
		cb := streamCallback{
			SourcePlugin: req.SourcePlugin,
			ChannelID:    req.ChannelID,
			TaskGroupID:  taskGroupID,
			Responder:    resolved.Alias,
		}
		agentResp, err := r.callAgentStream(ctx, resolved.PluginID, resolved.Model,
			remainder, req.ImageURLs, history, resolved.Alias, enrichedPrompt, cb)
		if err != nil {
			r.emitProgress(req.SourcePlugin, req.ChannelID, taskGroupID, "failed",
				fmt.Sprintf("Agent error: %v", err), nil)
			return
		}

		// Handle async tool response (e.g. video generation).
		if agentResp.Status == "processing" && agentResp.TaskID != "" {
			r.emitProgress(req.SourcePlugin, req.ChannelID, taskGroupID, "running",
				fmt.Sprintf("@%s started async task %s...", resolved.Alias, agentResp.TaskID), nil)
			asyncResp, asyncErr := r.waitForAsync(agentResp.TaskID, taskGroupID, req.SourcePlugin, req.ChannelID)
			if asyncErr != nil {
				r.emitProgress(req.SourcePlugin, req.ChannelID, taskGroupID, "failed",
					fmt.Sprintf("Agent error: %v", asyncErr), nil)
				return
			}
			agentResp = asyncResp
		}

		if r.debug {
			r.tracer.Record(reqTraceID, "", debugtrace.TypeFinalResponse, resolved.Alias, resolved.PluginID, "",
				agentResp.Response, toTraceAttachments(agentResp.Attachments), agentResp.Model,
				time.Since(callStart).Milliseconds(), "")
		}
		go r.memoryStore(sessionID, "assistant", agentResp.Response, resolved.Alias)

		rr := &relayResponse{
			Response:    agentResp.Response,
			Responder:   resolved.Alias,
			Model:       agentResp.Model,
			Backend:     agentResp.Backend,
			Usage:       agentResp.Usage,
			CostUSD:     agentResp.CostUSD,
			DurationMs:  time.Since(processStart).Milliseconds(),
			Attachments: agentResp.Attachments,
		}
		r.emitProgress(req.SourcePlugin, req.ChannelID, taskGroupID, "completed", "", rr)
		return
	}

	// 3. Route to the default agent.
	defaultAgent := r.resolveDefault(aliases)
	if defaultAgent == nil {
		r.emitProgress(req.SourcePlugin, req.ChannelID, taskGroupID, "failed",
			"no default agent found — assign the 'default' role to a persona in the persona manager", nil)
		return
	}

	r.emitProgress(req.SourcePlugin, req.ChannelID, taskGroupID, "running",
		fmt.Sprintf("Asking @%s...", defaultAgent.Alias), nil)

	callStart := time.Now()
	enrichedPrompt := r.enrichSystemPrompt(defaultAgent.SystemPrompt, aliases)
	cb := streamCallback{
		SourcePlugin: req.SourcePlugin,
		ChannelID:    req.ChannelID,
		TaskGroupID:  taskGroupID,
		Responder:    defaultAgent.Alias,
	}
	agentResp, err := r.callAgentStream(ctx, defaultAgent.PluginID, defaultAgent.Model,
		req.Message, req.ImageURLs, history, defaultAgent.Alias, enrichedPrompt, cb)
	if err != nil {
		r.emitProgress(req.SourcePlugin, req.ChannelID, taskGroupID, "failed",
			fmt.Sprintf("Agent error: %v", err), nil)
		return
	}

	if r.debug {
		r.tracer.Record(reqTraceID, "", debugtrace.TypeFinalResponse, defaultAgent.Alias, defaultAgent.PluginID, "",
			agentResp.Response, toTraceAttachments(agentResp.Attachments), agentResp.Model,
			time.Since(callStart).Milliseconds(), "")
	}
	go r.memoryStore(sessionID, "assistant", agentResp.Response, defaultAgent.Alias)

	rr := &relayResponse{
		Response:    agentResp.Response,
		Responder:   defaultAgent.Alias,
		Model:       agentResp.Model,
		Backend:     agentResp.Backend,
		Usage:       agentResp.Usage,
		CostUSD:     agentResp.CostUSD,
		DurationMs:  time.Since(processStart).Milliseconds(),
		Attachments: agentResp.Attachments,
	}
	r.emitProgress(req.SourcePlugin, req.ChannelID, taskGroupID, "completed", "", rr)
}

// --- Agent routing ---

// toTraceAttachments converts agent attachments to trace attachment format.
func toTraceAttachments(attachments []agentAttachment) []debugtrace.Attachment {
	if len(attachments) == 0 {
		return nil
	}
	out := make([]debugtrace.Attachment, len(attachments))
	for i, a := range attachments {
		out[i] = debugtrace.Attachment{MimeType: a.MimeType, ImageData: a.ImageData, Type: a.Type, URL: a.URL, Filename: a.Filename}
	}
	return out
}

// --- Agent routing ---

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

// agentChatRequest is the standard chat format used by all agent plugins.
type agentChatRequest struct {
	Message       string            `json:"message"`
	Model         string            `json:"model,omitempty"`
	ImageURLs     []string          `json:"image_urls,omitempty"`
	Conversation  []conversationMsg `json:"conversation"`
	AgentAlias    string            `json:"agent_alias,omitempty"`
	SystemPrompt  string            `json:"system_prompt,omitempty"`
}

type conversationMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type agentChatResponse struct {
	Response    string              `json:"response"`
	Model       string              `json:"model,omitempty"`
	Backend     string              `json:"backend,omitempty"`
	Usage       *agentUsage         `json:"usage,omitempty"`
	CostUSD     float64             `json:"cost_usd,omitempty"`
	Attachments []agentAttachment   `json:"attachments,omitempty"`
	// Async fields — present when plugin returns immediately with a task ID.
	Status      string              `json:"status,omitempty"`  // "processing" for async
	TaskID      string              `json:"task_id,omitempty"` // plugin's internal task ID
}

type agentUsage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	CachedTokens     int `json:"cached_tokens,omitempty"`
}

type agentAttachment struct {
	MimeType  string `json:"mime_type"`
	ImageData string `json:"image_data,omitempty"`
	Type      string `json:"type,omitempty"`
	URL       string `json:"url,omitempty"`
	Filename  string `json:"filename,omitempty"`
}

// callAgent sends a chat request to an agent plugin via the kernel route.
// The caller is responsible for resolving the persona/alias and passing the
// system prompt; callAgent no longer does its own persona lookup.
// Optional headers are merged into the outgoing request (e.g. call depth).
func (r *relay) callAgent(ctx context.Context, pluginID, model, message string, imageURLs []string, history []conversationMsg, agentAlias, systemPrompt string, extraHeaders ...map[string]string) (*agentChatResponse, error) {
	conversation := append(history, conversationMsg{Role: "user", Content: message})

	reqBody := agentChatRequest{
		Message:       message,
		Model:         model,
		ImageURLs:     imageURLs,
		Conversation:  conversation,
		AgentAlias:    agentAlias,
		SystemPrompt:  systemPrompt,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	var respBody []byte
	if len(extraHeaders) > 0 && extraHeaders[0] != nil {
		respBody, err = r.sdk.RouteToPluginWithHeaders(ctx, pluginID, "POST", "/chat", bytes.NewReader(body), extraHeaders[0])
	} else {
		respBody, err = r.sdk.RouteToPlugin(ctx, pluginID, "POST", "/chat", bytes.NewReader(body))
	}
	if err != nil {
		return nil, err
	}

	var chatResp agentChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &chatResp, nil
}

// streamCallback receives streaming events from callAgentStream.
type streamCallback struct {
	SourcePlugin string
	ChannelID    string
	TaskGroupID  string
	Responder    string
}

// callAgentStream sends a chat request to an agent plugin's streaming endpoint
// and forwards token chunks as relay:progress events. Returns the complete
// response when the stream finishes.
func (r *relay) callAgentStream(ctx context.Context, pluginID, model, message string, imageURLs []string, history []conversationMsg, agentAlias, systemPrompt string, cb streamCallback) (*agentChatResponse, error) {
	conversation := append(history, conversationMsg{Role: "user", Content: message})

	reqBody := agentChatRequest{
		Message:      message,
		Model:        model,
		ImageURLs:    imageURLs,
		Conversation: conversation,
		AgentAlias:   agentAlias,
		SystemPrompt: systemPrompt,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	resp, err := r.sdk.RouteToPluginStream(ctx, pluginID, "POST", "/chat/stream", bytes.NewReader(body))
	if err != nil {
		// Fall back to non-streaming if /chat/stream not available.
		log.Printf("[stream] streaming failed for %s, falling back to non-streaming: %v", pluginID, err)
		return r.callAgent(ctx, pluginID, model, message, imageURLs, history, agentAlias, systemPrompt)
	}
	defer resp.Body.Close()

	// Parse SSE stream and forward to messaging plugin.
	var fullText string
	var lastFlush time.Time
	flushInterval := 300 * time.Millisecond

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)

	var finalResp agentChatResponse

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			// Also check for event type lines to track tool calls.
			if strings.HasPrefix(line, "event: ") {
				continue
			}
			continue
		}

		// Parse the event type from the preceding "event:" line.
		// SSE spec: event line comes before data line. But since we process
		// line by line, we need to parse the event type from the data content.
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var ev struct {
			Content  string          `json:"content,omitempty"`
			Name     string          `json:"name,omitempty"`
			Error    string          `json:"error,omitempty"`
			Response string          `json:"response,omitempty"`
			Model    string          `json:"model,omitempty"`
			Backend  string          `json:"backend,omitempty"`
			Result   string          `json:"result,omitempty"`
			Usage    *agentUsage     `json:"usage,omitempty"`
			Attachments []agentAttachment `json:"attachments,omitempty"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}

		// Token event — accumulate and flush periodically.
		if ev.Content != "" {
			fullText += ev.Content
			if time.Since(lastFlush) >= flushInterval {
				r.emitProgress(cb.SourcePlugin, cb.ChannelID, cb.TaskGroupID, "streaming", fullText, nil)
				lastFlush = time.Now()
			}
		}

		// Tool call event — emit status update.
		if ev.Name != "" && ev.Result == "" && ev.Error == "" && ev.Response == "" {
			r.emitProgress(cb.SourcePlugin, cb.ChannelID, cb.TaskGroupID, "running",
				fmt.Sprintf("@%s is using %s...", cb.Responder, ev.Name), nil)
		}

		// Tool result event.
		if ev.Name != "" && (ev.Result != "" || ev.Error != "") {
			action := "got result from"
			if ev.Error != "" {
				action = "error from"
			}
			r.emitProgress(cb.SourcePlugin, cb.ChannelID, cb.TaskGroupID, "running",
				fmt.Sprintf("@%s %s %s", cb.Responder, action, ev.Name), nil)
		}

		// Done event — final response.
		if ev.Response != "" {
			finalResp = agentChatResponse{
				Response:    ev.Response,
				Model:       ev.Model,
				Backend:     ev.Backend,
				Usage:       ev.Usage,
				Attachments: ev.Attachments,
			}
		}

		// Error event.
		if ev.Error != "" && ev.Name == "" {
			return nil, fmt.Errorf("agent stream error: %s", ev.Error)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading stream: %w", err)
	}

	// Send final streaming flush if there's unsent text.
	if fullText != "" && time.Since(lastFlush) > 0 {
		r.emitProgress(cb.SourcePlugin, cb.ChannelID, cb.TaskGroupID, "streaming", fullText, nil)
	}

	// If we got a done event, use that. Otherwise construct from accumulated text.
	if finalResp.Response == "" {
		finalResp.Response = fullText
	}

	return &finalResp, nil
}

// callTool invokes a specific tool endpoint on a plugin. It resolves the tool
// by name from the discovery cache, substitutes path parameters, and calls
// the endpoint via the SDK route.
func (r *relay) callTool(ctx context.Context, pluginID, toolName string, params map[string]interface{}) ([]byte, error) {
	tools := discoverTools(r.sdk)
	var toolInfo *alias.ToolInfo
	for i := range tools {
		if tools[i].PluginID == pluginID && tools[i].Name == toolName {
			toolInfo = &tools[i]
			break
		}
	}
	if toolInfo == nil {
		return nil, fmt.Errorf("tool %q not found on plugin %s", toolName, pluginID)
	}

	endpoint := toolInfo.Endpoint
	if endpoint == "" {
		return nil, fmt.Errorf("tool %q on plugin %s has no endpoint", toolName, pluginID)
	}

	// Substitute :param placeholders in the endpoint path.
	// Convention: path param ":taskId" matches request param "task_id" (snake_case → camelCase).
	for key, val := range params {
		strVal := fmt.Sprintf("%v", val)
		// Try exact match first (e.g. :task_id), then camelCase (e.g. :taskId).
		placeholder := ":" + key
		if strings.Contains(endpoint, placeholder) {
			endpoint = strings.Replace(endpoint, placeholder, strVal, 1)
			continue
		}
		camel := snakeToCamel(key)
		placeholder = ":" + camel
		if strings.Contains(endpoint, placeholder) {
			endpoint = strings.Replace(endpoint, placeholder, strVal, 1)
		}
	}

	// Determine HTTP method: GET if no remaining body params, POST otherwise.
	method := "GET"
	bodyParams := make(map[string]interface{})
	for key, val := range params {
		placeholder := ":" + key
		camel := ":" + snakeToCamel(key)
		if !strings.Contains(toolInfo.Endpoint, placeholder) && !strings.Contains(toolInfo.Endpoint, camel) {
			bodyParams[key] = val
		}
	}

	var bodyReader *bytes.Reader
	if len(bodyParams) > 0 {
		method = "POST"
		body, err := json.Marshal(bodyParams)
		if err != nil {
			return nil, fmt.Errorf("marshal tool params: %w", err)
		}
		bodyReader = bytes.NewReader(body)
	}

	log.Printf("relay: calling tool %s on %s: %s %s", toolName, pluginID, method, endpoint)
	if bodyReader != nil {
		return r.sdk.RouteToPlugin(ctx, pluginID, method, endpoint, bodyReader)
	}
	return r.sdk.RouteToPlugin(ctx, pluginID, method, endpoint, nil)
}

// snakeToCamel converts snake_case to camelCase (e.g. "task_id" → "taskId").
func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) > 0 {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

// waitForAsync blocks until an async task completes. There is no fixed timeout —
// the background poll loop in the tool plugin drives liveness. As long as the
// remote API says "processing", we keep waiting.
func (r *relay) waitForAsync(taskID, taskGroupID, sourcePlugin, channelID string) (*agentChatResponse, error) {
	log.Printf("relay: async task %s (group=%s) — waiting for completion", taskID, taskGroupID)
	ch := r.registerAsyncWaiter(taskID, taskGroupID, sourcePlugin, channelID)
	defer r.removeAsyncWaiter(taskID)

	result := <-ch
	if result == nil {
		return nil, fmt.Errorf("async task %s: waiter closed without result", taskID)
	}
	return result, nil
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

// --- MCP tool endpoints ---

// maxAgentCallDepth is the maximum recursion depth for chat_to_agent calls.
const maxAgentCallDepth = 3

// handleMCP returns the tool definitions exposed by the relay.
func (r *relay) handleMCP(c *gin.Context) {
	// Build the list of available agent aliases for the description.
	aliases := r.routes.Aliases()
	var agentNames []string
	if aliases != nil {
		agentNames = aliases.ListAgentAliases()
	}

	agentList := "any available agent"
	if len(agentNames) > 0 {
		agentList = strings.Join(agentNames, ", ")
	}

	tools := []gin.H{
		{
			"name":        "chat_to_agent",
			"description": fmt.Sprintf("Delegate a task to another agent by alias. Available agents: %s. Use this when you need a specialist agent to handle part of a request.", agentList),
			"endpoint":    "/tools/chat_to_agent",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"agent_alias": gin.H{
						"type":        "string",
						"description": fmt.Sprintf("The alias of the agent to delegate to. Available: %s", agentList),
					},
					"message": gin.H{
						"type":        "string",
						"description": "The message or task to send to the agent",
					},
				},
				"required": []string{"agent_alias", "message"},
			},
		},
	}

	c.JSON(http.StatusOK, gin.H{"tools": tools})
}

// handleChatToAgent executes the chat_to_agent tool — delegates a message to another agent.
func (r *relay) handleChatToAgent(c *gin.Context) {
	// Recursion protection via call depth header.
	depth := 0
	if v := c.GetHeader("X-Teamagentica-Call-Depth"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			depth = n
		}
	}
	if depth >= maxAgentCallDepth {
		c.JSON(http.StatusOK, gin.H{
			"error": fmt.Sprintf("maximum agent delegation depth (%d) exceeded — cannot delegate further", maxAgentCallDepth),
		})
		return
	}

	var req struct {
		AgentAlias string `json:"agent_alias"`
		Message    string `json:"message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.AgentAlias == "" || req.Message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "agent_alias and message are required"})
		return
	}

	aliases := r.routes.Aliases()
	resolved := r.resolvePersona(req.AgentAlias, aliases)
	if resolved == nil {
		c.JSON(http.StatusOK, gin.H{
			"error": fmt.Sprintf("agent %q has no persona — create a persona to enable chat", req.AgentAlias),
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	enrichedPrompt := r.enrichSystemPrompt(resolved.SystemPrompt, aliases)

	// No conversation history for delegated calls — each delegation is stateless.
	// Propagate incremented call depth to prevent infinite recursion.
	depthHeaders := map[string]string{
		"X-Teamagentica-Call-Depth": strconv.Itoa(depth + 1),
	}
	agentResp, err := r.callAgent(ctx, resolved.PluginID, resolved.Model,
		req.Message, nil, nil, resolved.Alias, enrichedPrompt, depthHeaders)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": fmt.Sprintf("agent %s error: %v", req.AgentAlias, err)})
		return
	}

	result := gin.H{
		"response": agentResp.Response,
		"agent":    req.AgentAlias,
	}
	if agentResp.Model != "" {
		result["model"] = agentResp.Model
	}
	if len(agentResp.Attachments) > 0 {
		result["attachments"] = agentResp.Attachments
	}

	c.JSON(http.StatusOK, result)
}

// --- Config & routing management endpoints ---

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
		"workspace_mappings": r.routes.ListWorkspaces(),
	})
}

// routeMapSchema returns a read-only snapshot of the relay's routing state.
func (r *relay) routeMapSchema() map[string]interface{} {
	r.mu.RLock()
	activeConns := make([]string, 0, len(r.conns))
	for id := range r.conns {
		activeConns = append(activeConns, id)
	}
	r.mu.RUnlock()

	return map[string]interface{}{
		"active_connections": len(activeConns),
		"workspace_bridges":  activeConns,
		"workspace_mappings": r.routes.ListWorkspaces(),
	}
}

func main() {
	sdkCfg := pluginsdk.LoadConfig()
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8081

	var relayRef *relay

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         getHostname(),
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		SchemaFunc: func() map[string]interface{} {
			schema := map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
			if relayRef != nil {
				schema["route_map"] = relayRef.routeMapSchema()
			}
			return schema
		},
	})
	r := newRelay(sdkClient)
	relayRef = r

	// Refresh all aliases from the registry (used for ready + initial fetch).
	refreshAliases := func() {
		entries, err := sdkClient.FetchAliases()
		if err != nil {
			log.Printf("alias refresh failed: %v", err)
			return
		}
		r.routes.SetAliases(alias.NewAliasMap(entries))
		log.Printf("Aliases refreshed: %d entries", len(entries))

		r.personas.mu.Lock()
		r.personas.fetchedAt = time.Time{}
		r.personas.mu.Unlock()
	}

	// Patch a single alias on create/update/delete events.
	sdkClient.OnEvent("alias-registry:update", pluginsdk.NewTimedDebouncer(2*time.Second, func(event pluginsdk.EventCallback) {
		var detail struct {
			Action string `json:"action"`
			Alias  struct {
				Name     string `json:"name"`
				Type     string `json:"type"`
				Plugin   string `json:"plugin"`
				Model    string `json:"model"`
			} `json:"alias"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("alias-registry:update parse error: %v", err)
			return
		}

		am := r.routes.Aliases()
		if am == nil {
			refreshAliases()
			return
		}

		if detail.Action == "deleted" {
			am.Remove(detail.Alias.Name)
			log.Printf("Alias removed: %s", detail.Alias.Name)
		} else {
			target := detail.Alias.Plugin
			if detail.Alias.Model != "" {
				target += ":" + detail.Alias.Model
			}
			var caps []string
			switch detail.Alias.Type {
			case "agent":
				caps = []string{"agent:chat"}
			case "tool_agent":
				caps = []string{"agent:tool"}
			default:
				caps = []string{"tool:mcp"}
			}
			am.Set(detail.Alias.Name, alias.TargetFromInfo(target, caps))
			log.Printf("Alias %s: %s → %s", detail.Action, detail.Alias.Name, target)
		}

		r.personas.mu.Lock()
		r.personas.fetchedAt = time.Time{}
		r.personas.mu.Unlock()
	}))

	// Re-fetch aliases when the registry signals it's ready (handles startup ordering).
	sdkClient.OnEvent("alias-registry:ready", pluginsdk.NewTimedDebouncer(1*time.Second, func(event pluginsdk.EventCallback) {
		refreshAliases()
	}))

	sdkClient.Start(context.Background())

	entries, err := sdkClient.FetchAliases()
	if err != nil {
		log.Printf("Initial alias fetch failed: %v (will update via alias-registry:ready event)", err)
	} else {
		r.routes.SetAliases(alias.NewAliasMap(entries))
		log.Printf("Loaded %d aliases", len(entries))
	}

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Printf("Failed to fetch relay config: %v (using defaults)", err)
	}

	if v := pluginConfig["TASK_TIMEOUT_SECONDS"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			r.taskTimeoutSeconds = n
		}
	}
	if v := pluginConfig["PLUGIN_DEBUG"]; v == "true" {
		r.debug = true
		t, err := debugtrace.Open("/data/relay_traces.db")
		if err != nil {
			log.Printf("failed to open trace db: %v", err)
		} else {
			r.tracer = t
		}
		log.Printf("Debug mode enabled")
	}

	sdkClient.OnEvent("config:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var detail struct {
			Config map[string]string `json:"config"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("config:update parse error: %v", err)
			return
		}

		if v, ok := detail.Config["TASK_TIMEOUT_SECONDS"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				r.mu.Lock()
				r.taskTimeoutSeconds = n
				r.mu.Unlock()
				log.Printf("TASK_TIMEOUT_SECONDS updated: %d", n)
			}
		}

		if v, ok := detail.Config["PLUGIN_DEBUG"]; ok {
			enabled := v == "true"
			r.mu.Lock()
			r.debug = enabled
			if enabled && r.tracer == nil {
				t, err := debugtrace.Open("/data/relay_traces.db")
				if err != nil {
					log.Printf("failed to open trace db: %v", err)
				} else {
					r.tracer = t
				}
			} else if !enabled && r.tracer != nil {
				r.tracer.Close()
				r.tracer = nil
			}
			r.mu.Unlock()
			log.Printf("PLUGIN_DEBUG updated: %v", enabled)
		}
	}))

	// Handle progress updates from async plugins (e.g. seedance webhook callbacks).
	// Forward to the source messaging plugin and resolve any waiting async tasks.
	sdkClient.OnEvent("relay:task:progress", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var update struct {
			TaskID      string `json:"task_id"`
			Status      string `json:"status"`
			Message     string `json:"message"`
			VideoURL    string `json:"video_url,omitempty"`
			Attachments []struct {
				Type     string `json:"type,omitempty"`
				MimeType string `json:"mime_type,omitempty"`
				URL      string `json:"url,omitempty"`
				Filename string `json:"filename,omitempty"`
			} `json:"attachments,omitempty"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &update); err != nil {
			log.Printf("relay: failed to parse relay:task:progress: %v", err)
			return
		}

		log.Printf("relay: progress update task=%s status=%s message=%s", update.TaskID, update.Status, update.Message)

		// If an async task is waiting for this result, deliver it.
		if update.Status == "completed" || update.Status == "failed" {
			resp := &agentChatResponse{
				Response: update.Message,
			}
			if update.Status == "completed" {
				for _, att := range update.Attachments {
					resp.Attachments = append(resp.Attachments, agentAttachment{
						Type:     att.Type,
						MimeType: att.MimeType,
						URL:      att.URL,
						Filename: att.Filename,
					})
				}
			}
			if r.resolveAsyncWaiter(update.TaskID, resp) {
				log.Printf("relay: resolved async waiter for task %s (status=%s)", update.TaskID, update.Status)
			}
		}

		// Forward progress to the messaging plugin that initiated this task.
		// Look up session context from the async waiter first; fall back to
		// the global last-session if no waiter is registered.
		taskGroupID, sourcePlugin, channelID, found := r.lookupAsyncWaiter(update.TaskID)
		if !found {
			r.lastSessionMu.RLock()
			sourcePlugin = r.lastSourcePlugin
			channelID = r.lastChannelID
			r.lastSessionMu.RUnlock()
		}

		if sourcePlugin == "" {
			log.Printf("relay: no active session to forward progress to")
			return
		}

		// Forward as a "running" progress update so the UI shows it as an
		// intermediate status — not "completed"/"failed", which would cause
		// the messaging plugin to store a final message prematurely.
		// The real final response comes from the agent's streamed reply.
		forwardStatus := "running"
		forwardMessage := update.Message
		if update.Status == "completed" {
			forwardMessage = fmt.Sprintf("Task completed: %s", update.Message)
		} else if update.Status == "failed" {
			forwardMessage = fmt.Sprintf("Task failed: %s", update.Message)
		}

		payload, _ := json.Marshal(map[string]interface{}{
			"task_group_id": taskGroupID,
			"channel_id":    channelID,
			"task_id":       update.TaskID,
			"status":        forwardStatus,
			"message":       forwardMessage,
		})
		sdkClient.ReportAddressedEvent("relay:progress", string(payload), sourcePlugin)
		log.Printf("relay: forwarded progress to %s channel=%s group=%s", sourcePlugin, channelID, taskGroupID)
	}))

	ginRouter := gin.Default()

	ginRouter.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	ginRouter.POST("/chat", r.handleChat)
	ginRouter.GET("/mcp", r.handleMCP)
	ginRouter.POST("/tools/chat_to_agent", r.handleChatToAgent)

	ginRouter.POST("/config/workspace/map", r.handleMapWorkspace)
	ginRouter.POST("/config/workspace/unmap", r.handleUnmapWorkspace)

	ginRouter.GET("/status", r.handleStatus)

	ginRouter.GET("/debug/traces", func(c *gin.Context) {
		if r.tracer == nil {
			c.JSON(http.StatusOK, gin.H{"error": "debug mode is off, enable PLUGIN_DEBUG to start tracing"})
			return
		}
		limit := 50
		if v := c.Query("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		rows, err := r.tracer.ListRequests(limit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, rows)
	})

	ginRouter.GET("/debug/traces/:request_id", func(c *gin.Context) {
		if r.tracer == nil {
			c.JSON(http.StatusOK, gin.H{"error": "debug mode is off"})
			return
		}
		rows, err := r.tracer.GetTrace(c.Param("request_id"))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, rows)
	})

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", defaultPort),
		Handler: ginRouter,
	}

	go func() {
		time.Sleep(500 * time.Millisecond)
		sdkClient.ReportEvent("relay:ready", "accepting chat requests")
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
