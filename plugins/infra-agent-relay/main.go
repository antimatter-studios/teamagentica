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
	"strings"
	"sync"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-relay/internal/bridge"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-relay/internal/debugtrace"
	"github.com/antimatter-studios/teamagentica/plugins/infra-agent-relay/internal/router"
	"github.com/gin-gonic/gin"
)

// relay is the central message routing and orchestration service.
// Messaging plugins send messages here; the relay routes to a coordinator,
// which returns a JSON DAG plan; the relay executes the plan and returns
// the final result.
type relay struct {
	mu                    sync.RWMutex
	conns                 map[string]*bridge.Client // workspaceID → TCP connection
	routes                *router.Table
	sdk                   *pluginsdk.Client
	allowFirstAsDefault   bool // true when DEFAULT_COORDINATOR is unset at startup
	maxOrchestrationTasks int
	taskTimeoutSeconds    int
	debug                 bool
	tracer                *debugtrace.Recorder
	personas              *personaCache
	memoryPluginID        string // cached plugin ID for infra-agent-memory (empty = not available)
	memoryMu              sync.RWMutex
	memoryCheckedAt       time.Time
}

// personaInfo holds a cached persona definition from infra-alias-registry.
type personaInfo struct {
	Alias        string `json:"alias"`
	SystemPrompt string `json:"system_prompt"`
	BackendAlias string `json:"backend_alias"`
	Model        string `json:"model"`
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
		maxOrchestrationTasks: 20,
		taskTimeoutSeconds:    120,
		personas:              &personaCache{ttl: 60 * time.Second},
	}
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

	plugins, err := r.sdk.SearchPlugins("tool:aliases")
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

// mergedAliases returns an AliasMap that overlays persona aliases on top of
// kernel aliases. Personas with a backend_alias become routable without a
// corresponding kernel alias entry. Personas without a backend_alias are still
// cached for system prompt injection but are not added as routing entries.
func (r *relay) mergedAliases(kernelAliases *alias.AliasMap) *alias.AliasMap {
	personas := r.fetchPersonas()
	if len(personas) == 0 {
		return kernelAliases
	}

	merged := kernelAliases
	for _, p := range personas {
		if p.BackendAlias == "" {
			continue
		}
		backendTarget := kernelAliases.Resolve(p.BackendAlias)
		if backendTarget == nil {
			continue
		}
		model := p.Model
		if model == "" {
			model = backendTarget.Model
		}
		merged = merged.With(p.Alias, alias.Target{
			PluginID: backendTarget.PluginID,
			Model:    model,
			Type:     alias.TargetAgent,
		})
	}
	return merged
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
	Attachments []agentAttachment `json:"attachments,omitempty"` // media attachments from the agent
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

	// Session ID is unique per source plugin + channel.
	sessionID := req.SourcePlugin + ":" + req.ChannelID

	// Fetch conversation history from memory plugin (if available).
	history := r.memoryGetHistory(c.Request.Context(), sessionID)

	// Store the incoming user message.
	go r.memoryStore(sessionID, "user", req.Message, "")

	var reqTraceID string
	if r.debug {
		reqTraceID = debugtrace.NewRequestID()
		r.tracer.Record(reqTraceID, "", debugtrace.TypeRequest, "", req.SourcePlugin, "",
			fmt.Sprintf("source=%s channel=%s message=%s", req.SourcePlugin, req.ChannelID, req.Message),
			nil, "", 0, "")
	}

	// 1. Check if this channel is mapped to a workspace bridge.
	if ws := r.routes.GetWorkspace(req.SourcePlugin, req.ChannelID); ws != nil {
		r.routeToWorkspace(c, ws, req)
		return
	}

	// 2. Check for @alias prefix in message — direct routing, bypasses orchestration.
	aliases := r.mergedAliases(r.routes.Aliases())
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
			callStart := time.Now()
			agentResp, responder, err := r.routeToAgent(c.Request.Context(), result.Target.PluginID, result.Target.Model,
				result.Remainder, req.ImageURLs, history, false, result.Alias)
			if err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("agent: %v", err)})
				return
			}
			if r.debug {
				r.tracer.Record(reqTraceID, "", debugtrace.TypeFinalResponse, responder, result.Target.PluginID, "",
					agentResp.Response, toTraceAttachments(agentResp.Attachments), agentResp.Model,
					time.Since(callStart).Milliseconds(), "")
			}
			go r.memoryStore(sessionID, "assistant", agentResp.Response, responder)
			c.JSON(http.StatusOK, relayResponse{
				Response:    agentResp.Response,
				Responder:   responder,
				Model:       agentResp.Model,
				Backend:     agentResp.Backend,
				Usage:       agentResp.Usage,
				CostUSD:     agentResp.CostUSD,
				Attachments: agentResp.Attachments,
			})
			return
		}
	}

	// 3. Route through coordinator with DAG orchestration.
	coordinator := r.routes.GetCoordinator(req.SourcePlugin)
	if coordinator == nil {
		coordinator = r.routes.GetDefaultCoordinator()
	}
	if coordinator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no coordinator configured for " + req.SourcePlugin})
		return
	}

	coordAlias := ""
	if aliases != nil {
		coordAlias = aliases.FindAliasByPluginID(coordinator.PluginID)
	}
	if coordAlias == "" {
		coordAlias = coordinator.PluginID
	}

	response, responder, orchResp, err := r.orchestrate(c.Request.Context(), coordinator, coordAlias, req.Message, req.ImageURLs, aliases, reqTraceID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	if r.debug {
		var finalAttach []debugtrace.Attachment
		if orchResp != nil {
			finalAttach = toTraceAttachments(orchResp.Attachments)
		}
		r.tracer.Record(reqTraceID, "", debugtrace.TypeFinalResponse, responder, "", "",
			response, finalAttach, "", 0, "")
	}

	rr := relayResponse{
		Response:  response,
		Responder: responder,
	}
	if orchResp != nil {
		rr.Model = orchResp.Model
		rr.Backend = orchResp.Backend
		rr.Usage = orchResp.Usage
		rr.CostUSD = orchResp.CostUSD
		rr.Attachments = orchResp.Attachments
	}
	c.JSON(http.StatusOK, rr)
}

// --- DAG Orchestration ---

// taskPlan is the JSON structure the coordinator outputs.
type taskPlan struct {
	Tasks []dagTask `json:"tasks"`
}

// dagTask is a single task in the coordinator's plan.
type dagTask struct {
	ID        string   `json:"id"`
	Alias     string   `json:"alias"`
	Prompt    string   `json:"prompt"`
	DependsOn []string `json:"depends_on"`
}

// parseCoordinatorPlan extracts a taskPlan from a coordinator response.
// Handles raw JSON or ```json ... ``` fenced blocks.
// Returns (nil, false) if the response is a plain text answer with no plan.
func parseCoordinatorPlan(response string) (*taskPlan, bool) {
	s := strings.TrimSpace(response)

	// Extract from ```json ... ``` fence if present.
	if idx := strings.Index(s, "```json"); idx >= 0 {
		start := idx + len("```json")
		rest := s[start:]
		if end := strings.Index(rest, "```"); end >= 0 {
			s = strings.TrimSpace(rest[:end])
		}
	}

	// Must look like a JSON object.
	if !strings.HasPrefix(s, "{") {
		return nil, false
	}

	var plan taskPlan
	if err := json.Unmarshal([]byte(s), &plan); err != nil {
		return nil, false
	}
	if len(plan.Tasks) == 0 {
		return nil, false
	}
	return &plan, true
}

// interpolate substitutes {taskID} placeholders with completed task results.
func interpolate(prompt string, results map[string]string) string {
	for id, result := range results {
		prompt = strings.ReplaceAll(prompt, "{"+id+"}", result)
	}
	return prompt
}

// detectCycle returns true if the task DAG contains a circular dependency.
func detectCycle(tasks []dagTask) bool {
	deps := make(map[string][]string, len(tasks))
	for _, t := range tasks {
		deps[t.ID] = t.DependsOn
	}

	visited := make(map[string]bool, len(tasks))
	inStack := make(map[string]bool, len(tasks))

	var hasCycle func(id string) bool
	hasCycle = func(id string) bool {
		visited[id] = true
		inStack[id] = true
		for _, dep := range deps[id] {
			if !visited[dep] {
				if hasCycle(dep) {
					return true
				}
			} else if inStack[dep] {
				return true
			}
		}
		inStack[id] = false
		return false
	}

	for _, t := range tasks {
		if !visited[t.ID] {
			if hasCycle(t.ID) {
				return true
			}
		}
	}
	return false
}

// orchestrate calls the coordinator, parses its JSON DAG plan, executes tasks
// in topological order (parallel where possible), and returns the final result.
func (r *relay) orchestrate(
	ctx context.Context,
	coordinator *router.CoordinatorRoute,
	coordAlias string,
	message string,
	imageURLs []string,
	aliases *alias.AliasMap,
	reqTraceID string,
) (string, string, *agentChatResponse, error) {

	// Call coordinator in orchestration mode.
	coordStart := time.Now()
	if r.debug {
		r.tracer.Record(reqTraceID, "", debugtrace.TypeCoordinatorCall, coordAlias, coordinator.PluginID, "",
			message, nil, coordinator.Model, 0, "")
	}
	coordResp, err := r.callAgent(ctx, coordinator.PluginID, coordinator.Model,
		message, imageURLs, nil, true, coordAlias)
	if err != nil {
		return "", "", nil, fmt.Errorf("coordinator: %w", err)
	}

	if r.debug {
		r.tracer.Record(reqTraceID, "", debugtrace.TypeCoordinatorResponse, coordAlias, coordinator.PluginID, "",
			coordResp.Response, toTraceAttachments(coordResp.Attachments), coordResp.Model,
			time.Since(coordStart).Milliseconds(), "")
	}

	// Parse plan — if no JSON DAG, coordinator answered directly.
	plan, isDag := parseCoordinatorPlan(coordResp.Response)
	if !isDag {
		return coordResp.Response, coordAlias, coordResp, nil
	}

	if r.debug {
		planJSON, _ := json.Marshal(plan)
		r.tracer.Record(reqTraceID, "", debugtrace.TypeDAGPlan, coordAlias, "", "",
			string(planJSON), nil, "", 0, "")
	}

	// Validate plan.
	if len(plan.Tasks) > r.maxOrchestrationTasks {
		return "", "", nil, fmt.Errorf("plan has %d tasks, exceeds maximum of %d", len(plan.Tasks), r.maxOrchestrationTasks)
	}
	if detectCycle(plan.Tasks) {
		return "", "", nil, fmt.Errorf("circular dependency detected in task plan")
	}

	// Execute DAG — internally we only need the text for interpolation.
	// Track the last agentChatResponse for terminal task metadata.
	results := make(map[string]string, len(plan.Tasks))
	agentResps := make(map[string]*agentChatResponse, len(plan.Tasks))
	completed := make(map[string]bool, len(plan.Tasks))
	var mu sync.Mutex

	for len(completed) < len(plan.Tasks) {
		// Find tasks whose dependencies are all satisfied.
		var ready []dagTask
		for _, t := range plan.Tasks {
			if completed[t.ID] {
				continue
			}
			allDone := true
			for _, dep := range t.DependsOn {
				if !completed[dep] {
					allDone = false
					break
				}
			}
			if allDone {
				ready = append(ready, t)
			}
		}

		if len(ready) == 0 {
			return "", "", nil, fmt.Errorf("no tasks ready but %d incomplete — possible cycle missed", len(plan.Tasks)-len(completed))
		}

		// Snapshot results for interpolation before spawning goroutines.
		// All deps are complete so this snapshot is valid for all ready tasks.
		snapshot := make(map[string]string, len(results))
		for k, v := range results {
			snapshot[k] = v
		}

		// Run ready tasks in parallel.
		var wg sync.WaitGroup
		for _, task := range ready {
			wg.Add(1)
			go func(t dagTask) {
				defer wg.Done()

				prompt := interpolate(t.Prompt, snapshot)

				var callTraceID string
				taskStart := time.Now()
				targetPluginID := ""

				if r.debug {
					callTraceID = r.tracer.Record(reqTraceID, "", debugtrace.TypeTaskCall, t.Alias, "", t.ID,
						prompt, nil, "", 0, "")
				}

				taskCtx, cancel := context.WithTimeout(ctx, time.Duration(r.taskTimeoutSeconds)*time.Second)
				defer cancel()

				var result string
				var resp *agentChatResponse
				if t.Alias == "self" {
					targetPluginID = coordinator.PluginID
					// Call coordinator back in worker mode for synthesis.
					resp, err = r.callAgent(taskCtx, coordinator.PluginID, coordinator.Model,
						prompt, nil, nil, false, coordAlias)
					if err != nil {
						result = fmt.Sprintf("[error from self: %v]", err)
					} else {
						result = resp.Response
					}
				} else {
					target := aliases.Resolve(t.Alias)
					if target == nil || target.Type != alias.TargetAgent {
						result = fmt.Sprintf("[error: alias @%s not found or not an agent]", t.Alias)
					} else {
						targetPluginID = target.PluginID
						resp, err = r.callAgent(taskCtx, target.PluginID, target.Model,
							prompt, nil, nil, false, t.Alias)
						if err != nil {
							result = fmt.Sprintf("[error from @%s: %v]", t.Alias, err)
						} else {
							result = resp.Response
						}
					}
				}

				if r.debug {
					var traceErr string
					if err != nil {
						traceErr = err.Error()
					}
					var respAttach []debugtrace.Attachment
					respModel := ""
					if resp != nil {
						respAttach = toTraceAttachments(resp.Attachments)
						respModel = resp.Model
					}
					r.tracer.Record(reqTraceID, callTraceID, debugtrace.TypeTaskResponse, t.Alias, targetPluginID, t.ID,
						result, respAttach, respModel, time.Since(taskStart).Milliseconds(), traceErr)
				}

				mu.Lock()
				results[t.ID] = result
				if resp != nil {
					agentResps[t.ID] = resp
				}
				completed[t.ID] = true
				mu.Unlock()
			}(task)
		}
		wg.Wait()
	}

	// Find terminal tasks (nothing depends on them) — these are the outputs.
	dependedOn := make(map[string]bool, len(plan.Tasks))
	for _, t := range plan.Tasks {
		for _, dep := range t.DependsOn {
			dependedOn[dep] = true
		}
	}

	var terminals []dagTask
	for _, t := range plan.Tasks {
		if !dependedOn[t.ID] {
			terminals = append(terminals, t)
		}
	}

	// Collect all attachments from every task in the DAG.
	var allAttachments []agentAttachment
	for _, t := range plan.Tasks {
		if resp := agentResps[t.ID]; resp != nil && len(resp.Attachments) > 0 {
			allAttachments = append(allAttachments, resp.Attachments...)
		}
	}

	if len(terminals) == 1 {
		t := terminals[0]
		resp := results[t.ID]
		a := t.Alias
		if a == "self" {
			a = coordAlias
		}
		termResp := agentResps[t.ID]
		// If the terminal task itself has no attachments but other tasks do,
		// carry forward all collected attachments.
		if termResp != nil && len(termResp.Attachments) == 0 && len(allAttachments) > 0 {
			termResp.Attachments = allAttachments
		}
		return resp, a, termResp, nil
	}

	// Multiple terminal tasks — coordinator should have included a self synthesis task.
	// Concatenate as fallback.
	var sb strings.Builder
	for _, t := range terminals {
		a := t.Alias
		if a == "self" {
			a = coordAlias
		}
		fmt.Fprintf(&sb, "[@%s]: %s\n\n", a, results[t.ID])
	}
	// Build a synthetic response that carries forward all attachments.
	var synthResp *agentChatResponse
	if len(allAttachments) > 0 {
		synthResp = &agentChatResponse{Attachments: allAttachments}
	}
	return strings.TrimSpace(sb.String()), "relay", synthResp, nil
}

// toTraceAttachments converts agent attachments to trace attachment format.
func toTraceAttachments(attachments []agentAttachment) []debugtrace.Attachment {
	if len(attachments) == 0 {
		return nil
	}
	out := make([]debugtrace.Attachment, len(attachments))
	for i, a := range attachments {
		out[i] = debugtrace.Attachment{MimeType: a.MimeType, ImageData: a.ImageData}
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

// routeToAgent calls an agent plugin and returns the full response with responder alias set.
func (r *relay) routeToAgent(ctx context.Context, pluginID, model, message string, imageURLs []string, history []conversationMsg, isCoordinator bool, agentAlias string) (*agentChatResponse, string, error) {
	resp, err := r.callAgent(ctx, pluginID, model, message, imageURLs, history, isCoordinator, agentAlias)
	if err != nil {
		return nil, "", err
	}
	return resp, agentAlias, nil
}

// agentChatRequest is the standard chat format used by all agent plugins.
type agentChatRequest struct {
	Message       string            `json:"message"`
	Model         string            `json:"model,omitempty"`
	ImageURLs     []string          `json:"image_urls,omitempty"`
	Conversation  []conversationMsg `json:"conversation"`
	IsCoordinator bool              `json:"is_coordinator,omitempty"`
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
}

type agentUsage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	CachedTokens     int `json:"cached_tokens,omitempty"`
}

type agentAttachment struct {
	MimeType  string `json:"mime_type"`
	ImageData string `json:"image_data"`
}

// callAgent sends a chat request to an agent plugin via the kernel route.
func (r *relay) callAgent(ctx context.Context, pluginID, model, message string, imageURLs []string, history []conversationMsg, isCoordinator bool, agentAlias string) (*agentChatResponse, error) {
	// Inject persona system prompt for worker calls.
	systemPrompt := ""
	if !isCoordinator && agentAlias != "" {
		if p := r.lookupPersona(agentAlias); p != nil {
			systemPrompt = p.SystemPrompt
			if p.Model != "" && model == "" {
				model = p.Model
			}
		}
	}

	conversation := append(history, conversationMsg{Role: "user", Content: message})

	reqBody := agentChatRequest{
		Message:       message,
		Model:         model,
		ImageURLs:     imageURLs,
		Conversation:  conversation,
		IsCoordinator: isCoordinator,
		AgentAlias:    agentAlias,
		SystemPrompt:  systemPrompt,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	respBody, err := r.sdk.RouteToPlugin(ctx, pluginID, "POST", "/chat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	var chatResp agentChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &chatResp, nil
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

// coordinatorMapSchema returns a snapshot of current coordinator assignments for display.
func (r *relay) coordinatorMapSchema() map[string]string {
	coordinators := r.routes.ListCoordinators()
	defaultCoord := r.routes.GetDefaultCoordinator()
	aliases := r.routes.Aliases()

	coordMap := make(map[string]string)
	for sourcePlugin, coord := range coordinators {
		name := coord.PluginID
		if aliases != nil {
			if a := aliases.FindAliasByPluginID(coord.PluginID); a != "" {
				name = "@" + a
			}
		}
		coordMap[sourcePlugin] = name
	}
	if defaultCoord != nil {
		name := defaultCoord.PluginID
		if aliases != nil {
			if a := aliases.FindAliasByPluginID(defaultCoord.PluginID); a != "" {
				name = "@" + a
			}
		}
		coordMap["(default)"] = name
	}
	if len(coordMap) == 0 {
		coordMap["(none)"] = "no coordinators assigned"
	}
	return coordMap
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
			coordMap := map[string]string{"(none)": "no coordinators assigned"}
			if relayRef != nil {
				coordMap = relayRef.coordinatorMapSchema()
			}
			return map[string]interface{}{
				"config":          manifest.ConfigSchema,
				"coordinator_map": coordMap,
			}
		},
	})
	r := newRelay(sdkClient)
	relayRef = r

	// Subscribe to alias updates from infra-alias-registry.
	// Converts registry entries to AliasInfo and replaces the alias map.
	sdkClient.OnEvent("alias:update", pluginsdk.NewTimedDebouncer(2*time.Second, func(event pluginsdk.EventCallback) {
		var detail struct {
			Aliases []struct {
				Name        string `json:"name"`
				Type        string `json:"type"`
				Plugin      string `json:"plugin"`
				Provider    string `json:"provider"`
				Model       string `json:"model"`
				SystemPrompt string `json:"system_prompt"`
			} `json:"aliases"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("alias:update parse error: %v", err)
			return
		}

		infos := make([]alias.AliasInfo, 0, len(detail.Aliases))
		for _, a := range detail.Aliases {
			target := a.Plugin
			if a.Model != "" {
				target = a.Plugin + ":" + a.Model
			}
			// Map registry type to SDK capabilities for correct TargetType resolution.
			var caps []string
			switch a.Type {
			case "agent":
				caps = []string{"agent:chat"}
			case "tool_agent":
				// Preserve the specific agent:tool sub-capability if present in the plugin.
				// Fall back to generic agent:tool for SDK routing.
				caps = []string{"agent:tool"}
			default:
				caps = []string{"tool:mcp"}
			}
			infos = append(infos, alias.AliasInfo{
				Name:         a.Name,
				Target:       target,
				Capabilities: caps,
			})
		}

		r.routes.SetAliases(alias.NewAliasMap(infos))
		log.Printf("Aliases updated from registry: %d entries", len(infos))
	}))

	sdkClient.OnEvent("relay:coordinator", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var detail struct {
			SourcePlugin string `json:"source_plugin"`
			Alias        string `json:"alias"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("relay:coordinator parse error: %v", err)
			return
		}
		aliases := r.routes.Aliases()
		if aliases == nil {
			log.Printf("relay:coordinator: aliases not loaded yet, skipping %s → @%s", detail.SourcePlugin, detail.Alias)
			return
		}
		target := aliases.Resolve(detail.Alias)
		if target == nil || target.Type != alias.TargetAgent {
			log.Printf("relay:coordinator: alias @%s not found or not an agent", detail.Alias)
			return
		}
		r.routes.SetCoordinator(detail.SourcePlugin, target.PluginID, target.Model)
		log.Printf("coordinator set via event: %s → @%s (%s)", detail.SourcePlugin, detail.Alias, target.PluginID)

		r.mu.Lock()
		setDefault := r.allowFirstAsDefault
		if setDefault {
			r.allowFirstAsDefault = false
		}
		r.mu.Unlock()

		if setDefault {
			r.routes.SetDefaultCoordinator(target.PluginID, target.Model)
			log.Printf("first coordinator also set as default: @%s (%s)", detail.Alias, target.PluginID)
		}
	}))

	sdkClient.Start(context.Background())

	entries, err := sdkClient.FetchAliases()
	if err != nil {
		log.Printf("Initial alias fetch failed: %v (will update via events)", err)
	} else {
		r.routes.SetAliases(alias.NewAliasMap(entries))
		log.Printf("Loaded %d aliases", len(entries))
	}

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Printf("Failed to fetch relay config: %v (using defaults)", err)
	}

	if v := pluginConfig["DEFAULT_COORDINATOR"]; v != "" {
		aliases := r.routes.Aliases()
		if aliases != nil {
			if target := aliases.Resolve(v); target != nil && target.Type == alias.TargetAgent {
				r.routes.SetDefaultCoordinator(target.PluginID, target.Model)
				log.Printf("Default coordinator set from config: @%s (%s)", v, target.PluginID)
			} else {
				log.Printf("DEFAULT_COORDINATOR alias @%s not found, will use first registered coordinator", v)
				r.mu.Lock()
				r.allowFirstAsDefault = true
				r.mu.Unlock()
			}
		}
	} else {
		log.Printf("DEFAULT_COORDINATOR not set — first relay:coordinator event will become the default")
		r.mu.Lock()
		r.allowFirstAsDefault = true
		r.mu.Unlock()
	}

	if v := pluginConfig["MAX_ORCHESTRATION_TASKS"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			r.maxOrchestrationTasks = n
		}
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

		if v, ok := detail.Config["DEFAULT_COORDINATOR"]; ok {
			aliases := r.routes.Aliases()
			if aliases == nil {
				log.Printf("config:update DEFAULT_COORDINATOR: aliases not loaded yet")
				return
			}
			if v == "" {
				log.Printf("DEFAULT_COORDINATOR cleared")
				return
			}
			target := aliases.Resolve(v)
			if target == nil || target.Type != alias.TargetAgent {
				log.Printf("config:update DEFAULT_COORDINATOR: alias @%s not found", v)
				return
			}
			r.routes.SetDefaultCoordinator(target.PluginID, target.Model)
			log.Printf("Default coordinator updated: @%s (%s)", v, target.PluginID)
		}

		if v, ok := detail.Config["MAX_ORCHESTRATION_TASKS"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				r.mu.Lock()
				r.maxOrchestrationTasks = n
				r.mu.Unlock()
				log.Printf("MAX_ORCHESTRATION_TASKS updated: %d", n)
			}
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

	ginRouter := gin.Default()

	ginRouter.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	ginRouter.POST("/chat", r.handleChat)

	ginRouter.POST("/config/coordinator", r.handleSetCoordinator)
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
