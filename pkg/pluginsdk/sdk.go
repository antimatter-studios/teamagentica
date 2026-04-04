package pluginsdk

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
)

// DevVersion returns version with a build timestamp appended when running
// under air (binary name starts with "air-"). Each process start gets a
// unique stamp. Production binaries return the base version unchanged.
func DevVersion(base string) string {
	if strings.HasPrefix(filepath.Base(os.Args[0]), "air-") {
		return base + "-" + time.Now().Format("20060102_150405")
	}
	return base
}

// PluginDependencies declares what a plugin requires to function.
type PluginDependencies struct {
	Capabilities []string `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
}

// Registration holds the plugin's self-description sent to the kernel on boot.
type Registration struct {
	ID           string                       `json:"id"`
	Name         string                       `json:"name,omitempty"`
	Host         string                       `json:"host"`
	Port         int                          `json:"port"`
	Capabilities []string                     `json:"capabilities"`
	Version      string                       `json:"version"`
	Candidate    bool                         `json:"candidate,omitempty"` // true if running as a candidate container
	Dependencies    PluginDependencies           `json:"dependencies,omitempty"`
	Schema          map[string]interface{}       `json:"schema,omitempty"`
	ConfigSchema    map[string]ConfigSchemaField `json:"config_schema,omitempty"`
	WorkspaceSchema map[string]interface{}       `json:"workspace_schema,omitempty"`
	ChatCommands    []ChatCommand                `json:"chat_commands,omitempty"`
	SchemaFunc      SchemaFunc                   `json:"-"`
	ToolsFunc       func() interface{}           `json:"-"` // returns tool definitions for schema display
}

// EventCallback is the payload delivered by the kernel for subscribed events.
type EventCallback struct {
	EventType string `json:"event_type"`
	PluginID  string `json:"plugin_id"`
	Detail    string `json:"detail"`
	Timestamp string `json:"timestamp"`
	Seq       uint64 `json:"seq,omitempty"` // monotonic sequence number for ordering
}

// EventHandler is a callback for a subscribed event type.
type EventHandler func(event EventCallback)

// Client manages the plugin's relationship with the kernel.
type Client struct {
	config       Config
	registration Registration
	httpClient   *http.Client
	routeClient  *http.Client // longer timeout for RouteToPlugin (AI chat)
	stopCh       chan struct{}

	// Event handler dispatch — populated by OnEvent(), dispatched by EventHandler().
	eventDebouncers map[string]Debouncer
	eventMu         sync.RWMutex
	registeredCh    chan struct{} // closed after successful kernel registration

	// Peer registry — caches plugin addresses for direct P2P communication.
	// Populated from GET /api/plugins/registry on startup, kept fresh by
	// plugin:ready / plugin:stopped lifecycle events from the kernel.
	peers   map[string]peerEntry
	peersMu sync.RWMutex

	// Cached storage plugin discovery.
	storagePluginID string
	storageMu       sync.RWMutex

	// Event client — lazy-initialized by Events().
	eventClient     *EventClient
	eventClientOnce sync.Once
}

// PluginID returns the plugin's registered ID.
func (c *Client) PluginID() string {
	return c.registration.ID
}

// Events returns the plugin's EventClient for publishing and subscribing
// to events on the event bus. The EventClient is created once and reused.
func (c *Client) Events() *EventClient {
	c.eventClientOnce.Do(func() {
		c.eventClient = NewEventClient(c)
	})
	return c.eventClient
}

// NewClient creates a new SDK client.
// If TLS is enabled and cert/key/CA paths are set, configures mTLS on the HTTP client.
func NewClient(cfg Config, reg Registration) *Client {
	// SDK gets its own transport — never share http.DefaultTransport with
	// other libraries (e.g. discordgo) to avoid connection pool interference.
	transport := &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	if cfg.TLSCert != "" && cfg.TLSKey != "" && cfg.TLSCA != "" {
		tlsCfg, err := buildClientTLSConfig(cfg.TLSCert, cfg.TLSKey, cfg.TLSCA)
		if err != nil {
			log.Printf("pluginsdk: WARNING: failed to configure mTLS client: %v — falling back to plain HTTP", err)
		} else {
			transport.TLSClientConfig = tlsCfg
			log.Println("pluginsdk: mTLS client configured")
		}
	}

	// Short timeout for control-plane calls (register, heartbeat, config).
	httpClient := &http.Client{Timeout: 10 * time.Second, Transport: transport}

	// Long timeout for data-plane calls (RouteToPlugin — AI agent chat can take 2+ min).
	routeClient := &http.Client{Timeout: 120 * time.Second, Transport: transport}

	// Auto-set candidate flag from config if not already set.
	if cfg.Candidate {
		reg.Candidate = true
	}

	return &Client{
		config:       cfg,
		registration: reg,
		httpClient:   httpClient,
		routeClient:  routeClient,
		peers:        make(map[string]peerEntry),
		stopCh:       make(chan struct{}),
		registeredCh: make(chan struct{}),
	}
}

// TLSConfig returns the *tls.Config used by this client for outbound mTLS,
// or nil if TLS is not enabled. This allows other HTTP clients (e.g. a kernel
// REST client) to share the same mTLS configuration.
func (c *Client) TLSConfig() *tls.Config {
	if transport, ok := c.httpClient.Transport.(*http.Transport); ok && transport != nil {
		return transport.TLSClientConfig
	}
	return nil
}

// kernelURL returns the base URL for the kernel API.
func (c *Client) kernelURL() string {
	scheme := "http"
	if c.config.TLSCert != "" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%s", scheme, c.config.KernelHost, c.config.KernelPort)
}

// Start registers with the kernel and begins the heartbeat loop.
// Retries registration with exponential backoff (1s, 2s, 4s, 8s, max 30s).
// This is non-blocking. Plugins must mount SchemaHandler() and EventHandler()
// on their own router before calling Start().
func (c *Client) Start(ctx context.Context) {
	go func() {
		c.registerWithKernel(ctx)
		close(c.registeredCh)

		c.loadPeerRegistry()
		c.startHeartbeat(ctx)
	}()
}

// registerWithKernel registers with the kernel using exponential backoff.
// Blocks until registration succeeds, ctx is cancelled, or Stop() is called.
func (c *Client) registerWithKernel(ctx context.Context) {
	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		default:
		}

		if err := c.register(); err != nil {
			log.Printf("pluginsdk: registration failed: %v (retrying in %s)", err, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			case <-c.stopCh:
				return
			}
			backoff = time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
			continue
		}

		log.Printf("pluginsdk: registered with kernel as %s", c.registration.ID)
		return
	}
}

// startHeartbeat sends a heartbeat to the kernel every 30 seconds.
// Blocks until ctx is cancelled or Stop() is called.
func (c *Client) startHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case <-ticker.C:
			if err := c.heartbeat(); err != nil {
				log.Printf("pluginsdk: heartbeat failed: %v", err)
			}
		}
	}
}

// PublishEvent publishes a broadcast event to the platform event bus (infra-redis).
func (c *Client) PublishEvent(eventType, detail string) {
	payload := map[string]interface{}{
		"event_type": eventType,
		"source":     c.registration.ID,
		"detail":     detail,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := c.RouteToPlugin(ctx, "infra-redis", "POST", "/events/publish", bytes.NewReader(body)); err != nil {
		log.Printf("pluginsdk: ReportEvent failed: %v", err)
	}
}

// UsageReport holds usage data reported by plugins to the kernel.
type UsageReport struct {
	UserID       string `json:"user_id,omitempty"`
	Provider     string `json:"provider"`
	Model        string `json:"model,omitempty"`
	RecordType   string `json:"record_type,omitempty"`
	Status       string `json:"status,omitempty"`
	Prompt       string `json:"prompt,omitempty"`
	TaskID       string `json:"task_id,omitempty"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
	TotalTokens  int    `json:"total_tokens,omitempty"`
	CachedTokens int    `json:"cached_tokens,omitempty"`
	DurationMs   int64  `json:"duration_ms,omitempty"`
}

// ReportUsage sends a usage report via the event bus to infra-cost-tracking.
func (c *Client) ReportUsage(report UsageReport) {
	data, err := json.Marshal(report)
	if err != nil {
		log.Printf("sdk: ReportUsage marshal error: %v", err)
		return
	}
	c.PublishEventTo("usage:report", string(data), "infra-cost-tracking")
}

// PublishEventTo publishes an event targeted at a specific plugin via the event bus.
func (c *Client) PublishEventTo(eventType, detail, destination string) {
	payload := map[string]interface{}{
		"event_type": eventType,
		"source":     c.registration.ID,
		"target":     destination,
		"detail":     detail,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("pluginsdk: PublishEventTo marshal error: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := c.RouteToPlugin(ctx, "infra-redis", "POST", "/events/publish", bytes.NewReader(body)); err != nil {
		log.Printf("pluginsdk: PublishEventTo failed: %v", err)
	}
}

// SubscribeEvent registers this plugin as a subscriber for the given event type
// on the event bus (infra-redis). Call this from inside an
// OnPluginAvailable("infra:events", ...) callback to ensure the event bus is ready.
func (c *Client) SubscribeEvent(eventType string) {
	payload := map[string]string{
		"plugin_id":  c.registration.ID,
		"event_type": eventType,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("pluginsdk: failed to marshal subscribe payload: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := c.RouteToPlugin(ctx, "infra-redis", "POST", "/events/subscribe", bytes.NewReader(body)); err != nil {
		log.Printf("pluginsdk: failed to subscribe to %s: %v", eventType, err)
		return
	}
	log.Printf("pluginsdk: subscribed to %s", eventType)
}

// Unsubscribe removes interest in events of the given type.
func (c *Client) Unsubscribe(eventType string) error {
	payload := map[string]string{
		"id":         c.registration.ID,
		"event_type": eventType,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal unsubscribe: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.kernelURL()+"/api/plugins/unsubscribe", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("kernel returned status %d", resp.StatusCode)
	}
	return nil
}

// OnEvent registers a handler for the given event type. The handler is called
// when a matching event arrives at the plugin's POST /events endpoint.
//
// This only registers the handler locally — it does NOT subscribe to the event
// bus. To receive events, the plugin must also call SubscribeEvent() from inside
// an OnPluginAvailable("infra:events", ...) callback.
func (c *Client) OnEvent(eventType string, debouncer Debouncer) {
	c.eventMu.Lock()
	if c.eventDebouncers == nil {
		c.eventDebouncers = make(map[string]Debouncer)
	}
	c.eventDebouncers[eventType] = debouncer
	c.eventMu.Unlock()
}

// OnPluginAvailable calls fn when a plugin with the given capability is available.
// It performs an immediate lookup and, if not found, listens for plugin:registered
// events to catch late-starting plugins. fn is also called on re-registration
// (e.g. after a plugin restart). Safe to call before or after Start().
func (c *Client) OnPluginAvailable(capability string, fn func(PluginInfo)) {
	// Immediate check — plugin may already be running.
	go func() {
		// Wait for our own registration first so SearchPlugins works.
		<-c.registeredCh

		plugins, err := c.SearchPlugins(capability)
		if err == nil && len(plugins) > 0 {
			log.Printf("pluginsdk: plugin available for %s: %s", capability, plugins[0].ID)
			fn(plugins[0])
		}
	}()

	// Also listen for future registrations.
	c.OnEvent("plugin:registered", NewNullDebouncer(func(event EventCallback) {
		plugins, err := c.SearchPlugins(capability)
		if err != nil || len(plugins) == 0 {
			return
		}
		log.Printf("pluginsdk: plugin (re)available for %s: %s", capability, plugins[0].ID)
		fn(plugins[0])
	}))
}

// RegisterToolsWithMCP pushes this plugin's tool definitions to the MCP server.
// Call from an OnPluginAvailable("infra:mcp-server", ...) callback.
func (c *Client) RegisterToolsWithMCP(mcpPluginID string, tools interface{}) error {
	payload := map[string]interface{}{
		"plugin_id": c.registration.ID,
		"tools":     tools,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal tools: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = c.RouteToPlugin(ctx, mcpPluginID, "POST", "/tools/register", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("register tools with %s: %w", mcpPluginID, err)
	}
	log.Printf("pluginsdk: registered %s tools with MCP server %s", c.registration.ID, mcpPluginID)
	return nil
}

// buildSchemaJSON returns the schema map to serve on GET /schema.
// Uses Schema if set, otherwise builds from legacy ConfigSchema/WorkspaceSchema.
func (c *Client) buildSchemaJSON() map[string]interface{} {
	if c.registration.Schema != nil {
		return c.registration.Schema
	}
	schema := map[string]interface{}{}
	if c.registration.ConfigSchema != nil {
		schema["config"] = c.registration.ConfigSchema
	}
	if c.registration.WorkspaceSchema != nil {
		schema["workspace"] = c.registration.WorkspaceSchema
	}
	if len(c.registration.ChatCommands) > 0 {
		schema["chat_commands"] = c.registration.ChatCommands
	}
	if len(schema) == 0 {
		return nil
	}
	return schema
}

// handleEventCallback has been replaced by EventHandler() in helpers.go.
// Lifecycle events (plugin:ready, plugin:stopped, plugin:registry-sync) are
// now handled inline within EventHandler() via handleLifecycleEvent().

// ── Progress helpers ────────────────────────────────────────────────────────

// ProgressUpdate describes a status update from an async task.
type ProgressUpdate struct {
	TaskID      string `json:"task_id"`
	Status      string `json:"status"`      // "processing", "completed", "failed"
	Message     string `json:"message"`     // human-readable status text
	VideoURL    string `json:"video_url,omitempty"`
	Attachments []struct {
		Type     string `json:"type,omitempty"`
		MimeType string `json:"mime_type,omitempty"`
		URL      string `json:"url,omitempty"`
		Filename string `json:"filename,omitempty"`
	} `json:"attachments,omitempty"`
}

// ReportRelayProgress sends a progress update to the agent relay via addressed event.
// The relay can forward this to the appropriate messaging plugin.
func (c *Client) ReportRelayProgress(update ProgressUpdate) {
	payload, _ := json.Marshal(update)
	c.PublishEventTo("relay:task:progress", string(payload), "infra-agent-relay")
	log.Printf("pluginsdk: sent progress to relay: task=%s status=%s", update.TaskID, update.Status)
}

// FetchAliases retrieves the current alias list from the alias-registry plugin
// via the kernel's plugin-to-plugin routing. Returns entries suitable for
// alias.NewAliasMap or alias.Replace.
func (c *Client) FetchAliases() ([]alias.AliasInfo, error) {
	data, err := c.RouteToPlugin(context.Background(), "infra-alias-registry", "GET", "/aliases", nil)
	if err != nil {
		return nil, fmt.Errorf("fetch aliases: %w", err)
	}

	var result struct {
		Aliases []struct {
			Name   string `json:"name"`
			Plugin string `json:"plugin"`
			Model  string `json:"model"`
		} `json:"aliases"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode aliases: %w", err)
	}

	infos := make([]alias.AliasInfo, 0, len(result.Aliases))
	for _, a := range result.Aliases {
		target := a.Plugin
		if a.Model != "" {
			target = a.Plugin + ":" + a.Model
		}
		infos = append(infos, alias.AliasInfo{
			Name:   a.Name,
			Target: target,
		})
	}

	return infos, nil
}

// Stop deregisters from the kernel, shuts down the event server, and stops the heartbeat loop.
func (c *Client) Stop() {
	// Stop all debouncers.
	c.eventMu.RLock()
	for _, d := range c.eventDebouncers {
		d.Stop()
	}
	c.eventMu.RUnlock()

	if err := c.deregister(); err != nil {
		log.Printf("pluginsdk: deregister failed: %v", err)
	} else {
		log.Printf("pluginsdk: deregistered from kernel")
	}
	close(c.stopCh)
}

// register calls POST /api/plugins/register on the kernel.
func (c *Client) register() error {
	body, err := json.Marshal(c.registration)
	if err != nil {
		return fmt.Errorf("marshal registration: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.kernelURL()+"/api/plugins/register", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("kernel returned status %d", resp.StatusCode)
	}
	return nil
}

// heartbeat calls POST /api/plugins/heartbeat on the kernel.
// If the kernel responds with "re-register", the plugin re-sends its full
// registration to recover from a lost host/port (e.g. after kernel restart).
func (c *Client) heartbeat() error {
	body, err := json.Marshal(map[string]interface{}{"id": c.registration.ID, "candidate": c.registration.Candidate})
	if err != nil {
		return fmt.Errorf("marshal heartbeat: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.kernelURL()+"/api/plugins/heartbeat", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("kernel returned status %d", resp.StatusCode)
	}

	// Check if the kernel wants us to re-register (host/port was cleared).
	var result struct {
		Message string `json:"message"`
	}
	if json.NewDecoder(resp.Body).Decode(&result) == nil && result.Message == "re-register" {
		log.Printf("pluginsdk: kernel requested re-register — re-sending registration")
		if err := c.register(); err != nil {
			return fmt.Errorf("re-register failed: %w", err)
		}
		log.Printf("pluginsdk: re-registered with kernel as %s", c.registration.ID)
	}

	return nil
}

// deregister calls POST /api/plugins/deregister on the kernel.
func (c *Client) deregister() error {
	body, err := json.Marshal(map[string]string{"id": c.registration.ID})
	if err != nil {
		return fmt.Errorf("marshal deregister: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.kernelURL()+"/api/plugins/deregister", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("kernel returned status %d", resp.StatusCode)
	}
	return nil
}
