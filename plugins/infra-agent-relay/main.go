package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-relay/internal/bridge"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-relay/internal/debugtrace"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-relay/internal/router"
	"github.com/gin-gonic/gin"
)

// relay is the central message routing service.
// Messaging plugins send messages here; the relay resolves the target agent
// (via @mention or the default agent) and streams the response back.
type relay struct {
	mu                    sync.RWMutex
	conns                 map[string]*bridge.Client // workspaceID → TCP connection
	routes                *router.Table
	sdk                   *pluginsdk.Client
	taskTimeoutSeconds    int
	debug                 bool
	tracer                *debugtrace.Recorder
	agents                *agentCache
	memoryPluginID        string // cached plugin ID for infra-agent-memory-gateway (empty = not available)
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

// agentInfo holds a cached agent definition from infra-agent-registry.
type agentInfo struct {
	Alias        string `json:"alias"`
	Type         string `json:"type"`
	Plugin       string `json:"plugin"`
	SystemPrompt string `json:"system_prompt"`
	Model        string `json:"model"`
	IsDefault    *bool  `json:"is_default"`
}

// agentCache holds all agent definitions, loaded on startup and patched
// reactively via agent:update events. No TTL polling.
type agentCache struct {
	mu     sync.RWMutex
	agents map[string]agentInfo
}

func newRelay(sdk *pluginsdk.Client) *relay {
	return &relay{
		conns:                 make(map[string]*bridge.Client),
		routes:                router.NewTable(),
		sdk:                   sdk,
		taskTimeoutSeconds:    120,
		agents:                &agentCache{},
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

// refreshAgents fetches all agents from infra-agent-registry and replaces
// the cache. On failure, the existing cache is preserved (never overwrite good
// data with empty). Called once on startup and reactively from agent:update events.
func (r *relay) refreshAgents() {
	if r.sdk == nil {
		return
	}

	plugins, err := r.sdk.SearchPlugins("tool:agents")
	if err != nil || len(plugins) == 0 {
		return
	}

	pluginID := plugins[0].ID
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	body, err := r.sdk.RouteToPlugin(ctx, pluginID, "GET", "/agents", nil)
	if err != nil {
		log.Printf("relay: agent fetch from %s failed (keeping cached): %v", pluginID, err)
		return
	}

	var resp struct {
		Agents []agentInfo `json:"agents"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		log.Printf("relay: agent parse failed (keeping cached): %v", err)
		return
	}

	agents := make(map[string]agentInfo, len(resp.Agents))
	for _, p := range resp.Agents {
		agents[p.Alias] = p
	}

	r.agents.mu.Lock()
	r.agents.agents = agents
	r.agents.mu.Unlock()

	if len(agents) > 0 {
		log.Printf("relay: loaded %d agents from %s", len(agents), pluginID)
	}
}

// fetchAgents returns the current agent cache. The cache is populated on
// startup and kept fresh by agent:update events — no polling.
func (r *relay) fetchAgents() map[string]agentInfo {
	r.agents.mu.RLock()
	p := r.agents.agents
	r.agents.mu.RUnlock()
	if p != nil {
		return p
	}
	return map[string]agentInfo{}
}

// lookupAgent returns the agent for agentAlias, or nil if not found.
// On cache miss it refreshes once from the agent registry so newly-created
// agents are available immediately even if the event was lost or delayed.
func (r *relay) lookupAgent(agentAlias string) *agentInfo {
	if agentAlias == "" || r.sdk == nil {
		return nil
	}
	agents := r.fetchAgents()
	if p, ok := agents[agentAlias]; ok {
		return &p
	}
	// Cache miss — refresh once and retry.
	r.refreshAgents()
	agents = r.fetchAgents()
	if p, ok := agents[agentAlias]; ok {
		return &p
	}
	return nil
}

// resolvedTarget holds the result of agent-first, alias-fallback resolution.
type resolvedTarget struct {
	PluginID     string
	Model        string
	SystemPrompt string // from agent definition; empty for raw alias calls
	Alias        string // the name that was resolved
}

// resolveAgent looks up a name as an agent and resolves it to a concrete
// plugin + model. Returns nil if no agent exists for the name.
func (r *relay) resolveAgent(name string, aliases *alias.AliasMap) *resolvedTarget {
	// 1. Try agent lookup first (has system prompt, model override, etc.).
	if p := r.lookupAgent(name); p != nil && p.Plugin != "" {
		return &resolvedTarget{
			PluginID:     p.Plugin,
			Model:        p.Model,
			SystemPrompt: p.SystemPrompt,
			Alias:        name,
		}
	}

	// 2. Fall back to alias map if it's a chattable target (agent or agent-tool).
	// This allows tool-agents like nb2 to be addressed directly without an agent definition.
	if aliases != nil {
		if target := aliases.Resolve(name); target != nil && target.IsChatTarget() {
			return &resolvedTarget{
				PluginID: target.PluginID,
				Model:    target.Model,
				Alias:    name,
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
	var name, remainder string
	if spaceIdx < 0 {
		name = rest
	} else {
		name = rest[:spaceIdx]
		remainder = strings.TrimSpace(rest[spaceIdx+1:])
	}
	// Strip trailing punctuation so "@chat, hello" resolves to alias "chat".
	name = strings.TrimRight(name, ",.;:!?")
	if name == "" {
		return "", "", false
	}
	return strings.ToLower(name), remainder, true
}

// --- Memory integration ---

// memoryPlugin returns the plugin ID of the infra-agent-memory-gateway plugin, or "" if unavailable.
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

	plugins, err := r.sdk.SearchPlugins("^tool:memory$")
	if err != nil || len(plugins) == 0 {
		r.memoryPluginID = ""
	} else {
		r.memoryPluginID = plugins[0].ID
	}
	r.memoryCheckedAt = time.Now()
	return r.memoryPluginID
}

// memoryGetHistory fetches conversation context from the memory gateway.
// Uses LCM's get_context endpoint to retrieve recent messages + DAG summaries.
// Returns nil if the memory plugin is unavailable.
func (r *relay) memoryGetHistory(ctx context.Context, sessionID string) []conversationMsg {
	pluginID := r.memoryPlugin()
	if pluginID == "" {
		return nil
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"session_id": sessionID,
		"max_tokens": 100000,
	})

	body, err := r.sdk.RouteToPlugin(ctx, pluginID, "POST",
		"/mcp/get_context", bytes.NewReader(payload))
	if err != nil {
		log.Printf("relay: memory get_context failed: %v", err)
		return nil
	}

	var resp struct {
		Messages []struct {
			Role    string      `json:"role"`
			Content interface{} `json:"content"` // can be string or []block
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		log.Printf("relay: memory get_context parse failed: %v", err)
		return nil
	}

	msgs := make([]conversationMsg, 0, len(resp.Messages))
	for _, m := range resp.Messages {
		content := ""
		switch v := m.Content.(type) {
		case string:
			content = v
		default:
			// LCM returns structured content blocks for assistant messages — flatten.
			if b, err := json.Marshal(v); err == nil {
				content = string(b)
			}
		}
		if content != "" {
			msgs = append(msgs, conversationMsg{Role: m.Role, Content: content})
		}
	}
	return msgs
}

// memoryStore saves messages to the episodic memory (LCM) via the gateway.
// Fire-and-forget: errors are logged but not returned.
func (r *relay) memoryStore(sessionID, role, content, responder string) {
	pluginID := r.memoryPlugin()
	if pluginID == "" {
		return
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"session_id": sessionID,
		"messages": []map[string]string{
			{"role": role, "content": content},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := r.sdk.RouteToPlugin(ctx, pluginID, "POST",
		"/mcp/store_messages", bytes.NewReader(payload)); err != nil {
		log.Printf("relay: memory store_messages failed: %v", err)
	}
}

// memorySearchFacts searches semantic memory (Mem0) for facts relevant to the query.
// Returns a formatted string of relevant facts for injection into the system prompt.
func (r *relay) memorySearchFacts(ctx context.Context, query string) string {
	pluginID := r.memoryPlugin()
	if pluginID == "" {
		return ""
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"query":  query,
		"top_k":  10,
		"user_id": "global",
	})

	body, err := r.sdk.RouteToPlugin(ctx, pluginID, "POST",
		"/mcp/search_memories", bytes.NewReader(payload))
	if err != nil {
		log.Printf("relay: memory search_memories failed: %v", err)
		return ""
	}

	var resp struct {
		Results []struct {
			Text  string  `json:"memory"`
			Score float64 `json:"score"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || len(resp.Results) == 0 {
		return ""
	}

	var facts []string
	for _, r := range resp.Results {
		if r.Text != "" {
			facts = append(facts, "- "+r.Text)
		}
	}
	if len(facts) == 0 {
		return ""
	}
	return "## What you know about this user\n" + strings.Join(facts, "\n")
}

// memoryExtractFacts sends messages to Mem0 for semantic fact extraction.
// Fire-and-forget: runs async, errors are logged.
func (r *relay) memoryExtractFacts(sessionID string, messages []conversationMsg) {
	pluginID := r.memoryPlugin()
	if pluginID == "" {
		return
	}

	mem0Messages := make([]map[string]string, len(messages))
	for i, m := range messages {
		mem0Messages[i] = map[string]string{"role": m.Role, "content": m.Content}
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"messages": mem0Messages,
		"user_id":  "global",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := r.sdk.RouteToPlugin(ctx, pluginID, "POST",
		"/mcp/add_memory", bytes.NewReader(payload)); err != nil {
		log.Printf("relay: memory add_memory (fact extraction) failed: %v", err)
	}
}

// resolveDefault finds the agent marked as is_default and
// resolves its backend alias to a concrete plugin + model.
func (r *relay) resolveDefault(aliases *alias.AliasMap) *resolvedTarget {
	agents := r.fetchAgents()
	for _, p := range agents {
		if p.IsDefault != nil && *p.IsDefault {
			return r.resolveAgent(p.Alias, aliases)
		}
	}
	return nil
}

// --- Chat endpoint: the main entry point for all messaging plugins ---

// relayRequest is the envelope from messaging plugins.
type relayRequest struct {
	SourcePlugin string   `json:"source_plugin"`        // e.g. "messaging-discord"
	ChannelID    string   `json:"channel_id"`            // channel/group/chat ID
	Message      string   `json:"message"`               // user's message text
	ImageURLs    []string `json:"image_urls,omitempty"`  // attached media
	TaskGroupID  string   `json:"task_group_id,omitempty"` // caller-provided; relay uses this if set
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
	r.sdk.PublishEventTo(events.RelayProgress, string(data), sourcePlugin)
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

	// Validate @alias synchronously before accepting the task.
	// The async goroutine would catch this too, but the "completed" event
	// races with task registration in the messaging plugin and gets dropped.
	if name, remainder, ok := parseAtPrefix(req.Message); ok {
		aliases := r.routes.Aliases()
		if resolved := r.resolveAgent(name, aliases); resolved == nil {
			c.JSON(http.StatusOK, gin.H{
				"user_message": fmt.Sprintf("@%s has no agent definition — create an agent to enable chat", name),
			})
			return
		} else if remainder == "" {
			c.JSON(http.StatusOK, gin.H{
				"user_message": fmt.Sprintf("Usage: @%s <message>", resolved.Alias),
			})
			return
		}
	}

	// Use caller-provided task group ID if present, otherwise generate one.
	taskGroupID := req.TaskGroupID
	if taskGroupID == "" {
		taskGroupID = "tg-" + uuid.New().String()[:8]
	}

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

	// --- Timing helper ---
	var timings []string
	mark := func(label string) {
		timings = append(timings, fmt.Sprintf("%s=%dms", label, time.Since(processStart).Milliseconds()))
	}

	// Stage 1: Message received.
	r.emitProgress(req.SourcePlugin, req.ChannelID, taskGroupID, "thinking", "Message received, loading context...", nil)
	mark("received")

	// Store, fetch history, and search facts all in parallel.
	var history []conversationMsg
	var memoryFacts string
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		r.memoryStore(sessionID, "user", req.Message, "")
	}()
	go func() {
		defer wg.Done()
		history = r.memoryGetHistory(ctx, sessionID)
	}()
	go func() {
		defer wg.Done()
		factsCtx, factsCancel := context.WithTimeout(ctx, 1*time.Second)
		defer factsCancel()
		memoryFacts = r.memorySearchFacts(factsCtx, req.Message)
	}()
	wg.Wait()
	mark("memory")

	var reqTraceID string
	if r.debug {
		reqTraceID = debugtrace.NewRequestID()
		r.tracer.Record(reqTraceID, "", debugtrace.TypeRequest, "", req.SourcePlugin, "",
			fmt.Sprintf("source=%s channel=%s message=%s", req.SourcePlugin, req.ChannelID, req.Message),
			nil, "", 0, "")
	}

	// 2. Check for @name prefix — agent direct routing.
	aliases := r.routes.Aliases()
	if name, remainder, ok := parseAtPrefix(req.Message); ok {
		resolved := r.resolveAgent(name, aliases)
		if resolved == nil {
			// @prefix was used but no agent exists — tell the user.
			rr := &relayResponse{
				Response:  fmt.Sprintf("@%s has no agent definition — create an agent to enable chat", name),
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

		// Stage 2: Routing to agent.
		r.emitProgress(req.SourcePlugin, req.ChannelID, taskGroupID, "running",
			fmt.Sprintf("Sending to @%s...", resolved.Alias), nil)
		mark("routing")

		callStart := time.Now()
		enrichedPrompt := r.enrichSystemPrompt(resolved.Alias, resolved.SystemPrompt, aliases)
		if memoryFacts != "" {
			enrichedPrompt = enrichedPrompt + "\n\n" + memoryFacts
		}
		cb := streamCallback{
			SourcePlugin: req.SourcePlugin,
			ChannelID:    req.ChannelID,
			TaskGroupID:  taskGroupID,
			Responder:    resolved.Alias,
		}
		agentResp, err := r.callAgentWithRetry(ctx, resolved.PluginID, resolved.Model,
			remainder, req.ImageURLs, history, resolved.Alias, enrichedPrompt, cb)
		mark("agent_call")
		if err != nil {
			log.Printf("[timing] tg=%s FAILED %s | %s", taskGroupID, strings.Join(timings, " "), err)
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
		go r.memoryExtractFacts(sessionID, []conversationMsg{
			{Role: "user", Content: remainder},
			{Role: "assistant", Content: agentResp.Response},
		})

		mark("total")
		log.Printf("[timing] tg=%s %s", taskGroupID, strings.Join(timings, " "))

		responseText := agentResp.Response
		if agentResp.ChunksEmitted {
			// Text already delivered via message_chunk events — don't re-send.
			responseText = ""
		}
		rr := &relayResponse{
			Response:    responseText,
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
			"no default agent found — no agent is assigned as the default in the agent registry, use @aliases to speak to a specific agent", nil)
		return
	}

	// Stage 2: Routing to default agent.
	r.emitProgress(req.SourcePlugin, req.ChannelID, taskGroupID, "running",
		fmt.Sprintf("Sending to @%s...", defaultAgent.Alias), nil)
	mark("routing")

	callStart := time.Now()
	enrichedPrompt := r.enrichSystemPrompt(defaultAgent.Alias, defaultAgent.SystemPrompt, aliases)
	if memoryFacts != "" {
		enrichedPrompt = enrichedPrompt + "\n\n" + memoryFacts
	}
	cb := streamCallback{
		SourcePlugin: req.SourcePlugin,
		ChannelID:    req.ChannelID,
		TaskGroupID:  taskGroupID,
		Responder:    defaultAgent.Alias,
	}
	agentResp, err := r.callAgentWithRetry(ctx, defaultAgent.PluginID, defaultAgent.Model,
		req.Message, req.ImageURLs, history, defaultAgent.Alias, enrichedPrompt, cb)
	mark("agent_call")
	if err != nil {
		log.Printf("[timing] tg=%s FAILED %s | %s", taskGroupID, strings.Join(timings, " "), err)
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
	go r.memoryExtractFacts(sessionID, []conversationMsg{
		{Role: "user", Content: req.Message},
		{Role: "assistant", Content: agentResp.Response},
	})

	mark("total")
	log.Printf("[timing] tg=%s %s", taskGroupID, strings.Join(timings, " "))

	responseText := agentResp.Response
	if agentResp.ChunksEmitted {
		// Text already delivered via message_chunk events — don't re-send.
		responseText = ""
	}
	rr := &relayResponse{
		Response:    responseText,
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

// conversationMsg aliases the SDK type for relay-internal use.
type conversationMsg = pluginsdk.ConversationMsg

// agentUsage aliases the SDK type.
type agentUsage = pluginsdk.AgentUsage

// agentAttachment aliases the SDK type.
type agentAttachment = pluginsdk.AgentAttachment

// agentChatResponse wraps an agent's streaming result with relay-specific fields.
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
	// ChunksEmitted is true when response text was delivered via message_chunk
	// events during streaming. Internal-only; tells handleChat to skip the
	// response text in the completed event (it's already been sent).
	ChunksEmitted bool `json:"-"`
}

// buildAgentRequest constructs a pluginsdk.AgentChatRequest from relay parameters.
func buildAgentRequest(message, model string, imageURLs []string, history []conversationMsg, agentAlias, systemPrompt, sessionID string) pluginsdk.AgentChatRequest {
	conversation := append(history, conversationMsg{Role: "user", Content: message})
	return pluginsdk.AgentChatRequest{
		Message:      message,
		Model:        model,
		ImageURLs:    imageURLs,
		Conversation: conversation,
		AgentAlias:   agentAlias,
		SystemPrompt: systemPrompt,
		SessionID:    sessionID,
	}
}

// doneToResponse converts a pluginsdk.DoneEvent into an agentChatResponse.
func doneToResponse(d *pluginsdk.DoneEvent) *agentChatResponse {
	resp := &agentChatResponse{
		Response:    d.Response,
		Model:       d.Model,
		Backend:     d.Backend,
		Attachments: d.Attachments,
	}
	if d.Usage != nil {
		resp.Usage = d.Usage
	}
	return resp
}

// callAgent sends a chat request to an agent plugin via the SDK and collects
// the final response. Used for agent-to-agent delegation.
func (r *relay) callAgent(ctx context.Context, pluginID, model, message string, imageURLs []string, history []conversationMsg, agentAlias, systemPrompt string, extraHeaders ...map[string]string) (*agentChatResponse, error) {
	req := buildAgentRequest(message, model, imageURLs, history, agentAlias, systemPrompt, "")

	done, err := r.sdk.AgentChat(ctx, pluginID, req)
	if err != nil {
		return nil, err
	}

	return doneToResponse(done), nil
}

// streamCallback receives streaming events from callAgentStream.
type streamCallback struct {
	SourcePlugin string
	ChannelID    string
	TaskGroupID  string
	Responder    string
}

// isTransientErr returns true for errors that indicate the agent is temporarily
// unavailable (restarting, connection dropped mid-stream, etc.).
func isTransientErr(err error) bool {
	if err == nil {
		return false
	}
	// Unexpected EOF = stream broke mid-read (agent restarted/crashed).
	if errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(err.Error(), "unexpected EOF") {
		return true
	}
	// Connection reset/refused = agent container not (yet) listening.
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return true
	}
	// Catch-all string checks for wrapped errors.
	msg := err.Error()
	for _, sig := range []string{"connection reset", "connection refused", "EOF", "broken pipe", "no such host"} {
		if strings.Contains(msg, sig) {
			return true
		}
	}
	return false
}

const maxAgentRetries = 3

// callAgentWithRetry calls the agent via streaming (with non-streaming fallback)
// and retries on transient errors (e.g. agent restart mid-stream). Emits progress
// events so the user knows a retry is happening.
func (r *relay) callAgentWithRetry(
	ctx context.Context,
	pluginID, model, message string,
	imageURLs []string,
	history []conversationMsg,
	agentAlias, systemPrompt string,
	cb streamCallback,
) (*agentChatResponse, error) {
	delays := []time.Duration{3 * time.Second, 5 * time.Second, 8 * time.Second}

	var lastErr error
	for attempt := 0; attempt <= maxAgentRetries; attempt++ {
		if attempt > 0 {
			delay := delays[attempt-1]
			r.emitProgress(cb.SourcePlugin, cb.ChannelID, cb.TaskGroupID, "running",
				fmt.Sprintf("@%s disconnected, retrying in %ds (attempt %d/%d)...",
					cb.Responder, int(delay.Seconds()), attempt, maxAgentRetries), nil)
			log.Printf("[retry] %s attempt %d/%d after %v: %v", pluginID, attempt, maxAgentRetries, delay, lastErr)
			time.Sleep(delay)
		}

		resp, err := r.callAgentStream(ctx, pluginID, model, message, imageURLs, history, agentAlias, systemPrompt, cb)
		if err == nil {
			return resp, nil
		}

		lastErr = err
		if !isTransientErr(err) {
			return nil, err // non-transient (e.g. 400, bad model) — don't retry
		}
	}

	return nil, fmt.Errorf("%w (after %d retries)", lastErr, maxAgentRetries)
}

// callAgentStream sends a chat request to an agent plugin via the SDK's
// streaming helper and forwards token/tool events as relay:progress events.
// Returns the complete response when the stream finishes.
func (r *relay) callAgentStream(ctx context.Context, pluginID, model, message string, imageURLs []string, history []conversationMsg, agentAlias, systemPrompt string, cb streamCallback) (*agentChatResponse, error) {
	sessionID := cb.SourcePlugin + ":" + cb.ChannelID
	req := buildAgentRequest(message, model, imageURLs, history, agentAlias, systemPrompt, sessionID)

	stream, err := r.sdk.AgentChatStream(ctx, pluginID, req)
	if err != nil {
		return nil, err
	}

	var fullText string
	var pendingChunk string
	var chunksEmitted bool
	const chunkMaxLen = 500
	const chunkMinLen = 50
	var finalResp agentChatResponse

	emitChunk := func(text string) {
		payload := map[string]interface{}{
			"task_group_id": cb.TaskGroupID,
			"channel_id":    cb.ChannelID,
			"status":        "message_chunk",
			"message":       strings.ReplaceAll(text, "@@", "@"),
			"responder":     cb.Responder,
		}
		data, _ := json.Marshal(payload)
		r.sdk.PublishEventTo(events.RelayProgress, string(data), cb.SourcePlugin)
		chunksEmitted = true
	}

	flushChunk := func() {
		if len(pendingChunk) < chunkMinLen {
			return
		}
		emitChunk(pendingChunk)
		pendingChunk = ""
	}

	for ev := range stream {
		switch d := ev.Data.(type) {
		case pluginsdk.TokenEvent:
			fullText += d.Content
			pendingChunk += d.Content
			if len(pendingChunk) >= chunkMaxLen {
				flushChunk()
			}

		case pluginsdk.ToolCallEvent:
			// Flush any pending text before showing tool activity.
			if len(pendingChunk) >= chunkMinLen {
				flushChunk()
			}
			r.emitProgress(cb.SourcePlugin, cb.ChannelID, cb.TaskGroupID, "running",
				fmt.Sprintf("@%s is using %s...", cb.Responder, d.Name), nil)

		case pluginsdk.ToolResultEvent:
			action := "got result from"
			if d.Error != "" {
				action = "error from"
			}
			r.emitProgress(cb.SourcePlugin, cb.ChannelID, cb.TaskGroupID, "running",
				fmt.Sprintf("@%s %s %s", cb.Responder, action, d.Name), nil)

		case pluginsdk.DoneEvent:
			finalResp = *doneToResponse(&d)

		case pluginsdk.ErrorEvent:
			return nil, fmt.Errorf("agent stream error: %s", d.Error)
		}
	}

	// Flush any remaining pending chunk (no minimum length check — send whatever is left).
	if pendingChunk != "" {
		emitChunk(pendingChunk)
		pendingChunk = ""
	}

	// If we got a done event, use that. Otherwise construct from accumulated text.
	if finalResp.Response == "" {
		finalResp.Response = fullText
	}
	finalResp.ChunksEmitted = chunksEmitted

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

// toolDefs returns the MCP tool definitions for the relay.
func (r *relay) toolDefs() []gin.H {
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

	return []gin.H{
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
}

// handleMCP returns the tool definitions exposed by the relay.
func (r *relay) handleMCP(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": r.toolDefs()})
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
	resolved := r.resolveAgent(req.AgentAlias, aliases)
	if resolved == nil {
		c.JSON(http.StatusOK, gin.H{
			"error": fmt.Sprintf("agent %q has no agent definition — create an agent to enable chat", req.AgentAlias),
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	enrichedPrompt := r.enrichSystemPrompt(resolved.Alias, resolved.SystemPrompt, aliases)

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

		go r.refreshAgents()
	}

	// Patch a single alias on create/update/delete events.
	// Agent registry emits agent:update with alias-compatible payload for
	// both alias map patching and agent cache refresh.
	sdkClient.Events().On("agent:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var detail struct {
			Action string `json:"action"`
			Alias  struct {
				Name   string `json:"name"`
				Type   string `json:"type"`
				Plugin string `json:"plugin"`
				Model  string `json:"model"`
			} `json:"alias"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			// Fallback: just refresh everything.
			go r.refreshAgents()
			return
		}

		// Patch alias map in-place.
		am := r.routes.Aliases()
		if am == nil {
			refreshAliases()
		} else if detail.Alias.Name != "" {
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
		}

		go r.refreshAgents()
	}))

	// Re-fetch aliases when the agent registry signals it's ready (handles startup ordering).
	sdkClient.Events().On("agent:ready", pluginsdk.NewTimedDebouncer(1*time.Second, func(event pluginsdk.EventCallback) {
		refreshAliases()
	}))

	// Re-fetch aliases when any plugin is installed, uninstalled, or swapped.
	// The alias DB can change indirectly (e.g. a rename migration) with no
	// agent:update event, so lifecycle transitions are the safest trigger for
	// a full refresh. Debounced so rapid register/unregister doesn't storm the registry.
	aliasLifecycleDebouncer := pluginsdk.NewTimedDebouncer(500*time.Millisecond, func(event pluginsdk.EventCallback) {
		refreshAliases()
	})
	sdkClient.Events().On("plugin:ready", aliasLifecycleDebouncer)
	sdkClient.Events().On("plugin:stopped", aliasLifecycleDebouncer)

	sdkClient.Start(context.Background())

	entries, err := sdkClient.FetchAliases()
	if err != nil {
		log.Printf("Initial alias fetch failed: %v (retrying in background)", err)
		// Retry in background — agent registry or other deps may not be ready yet.
		go func() {
			for attempt := 1; attempt <= 30; attempt++ {
				time.Sleep(time.Duration(attempt*2) * time.Second)
				if attempt > 15 {
					time.Sleep(30 * time.Second)
				}
				e, err := sdkClient.FetchAliases()
				if err != nil {
					continue
				}
				r.routes.SetAliases(alias.NewAliasMap(e))
				log.Printf("Loaded %d aliases (after %d retries)", len(e), attempt)
				// Also refresh agents since they likely failed too.
				r.refreshAgents()
				return
			}
			log.Printf("WARNING: alias fetch never succeeded — relay has no aliases")
		}()
	} else {
		r.routes.SetAliases(alias.NewAliasMap(entries))
		log.Printf("Loaded %d aliases", len(entries))
	}

	// Load agents on startup; kept fresh by agent:update events.
	r.refreshAgents()

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

	events.OnConfigUpdate(sdkClient, func(p events.ConfigUpdatePayload) {
		if v, ok := p.Config["TASK_TIMEOUT_SECONDS"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				r.mu.Lock()
				r.taskTimeoutSeconds = n
				r.mu.Unlock()
				log.Printf("TASK_TIMEOUT_SECONDS updated: %d", n)
			}
		}

		if v, ok := p.Config["PLUGIN_DEBUG"]; ok {
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
	})

	// Handle progress updates from async plugins (e.g. seedance webhook callbacks).
	// Forward to the source messaging plugin and resolve any waiting async tasks.
	sdkClient.Events().On("relay:task:progress", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
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
		sdkClient.Events().PublishTo(events.RelayProgress, string(payload), sourcePlugin)
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

	// Push tools to MCP server when it becomes available.
	sdkClient.OnPluginAvailable("infra:mcp-server", func(p pluginsdk.PluginInfo) {
		if err := sdkClient.RegisterToolsWithMCP(p.ID, r.toolDefs()); err != nil {
			log.Printf("failed to register tools with MCP: %v", err)
		}
	})

	go func() {
		time.Sleep(500 * time.Millisecond)
		events.PublishRelayReady(sdkClient)
	}()

	sdkClient.ListenAndServe(defaultPort, ginRouter)
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "localhost"
	}
	return hostname
}
