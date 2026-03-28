package pluginsdk

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"

	"gopkg.in/yaml.v3"
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

// Manifest represents the plugin.yaml file — the single source of truth
// for a plugin's identity, capabilities, dependencies, and config schema.
type Manifest struct {
	ID           string                       `yaml:"id"`
	Name         string                       `yaml:"name"`
	Description  string                       `yaml:"description"`
	Group        string                       `yaml:"group"`
	Version      string                       `yaml:"version"`
	Image        string                       `yaml:"image"`
	Author       string                       `yaml:"author"`
	Tags         []string                     `yaml:"tags"`
	Capabilities []string                     `yaml:"capabilities"`
	Dependencies []string                     `yaml:"dependencies"`
	ConfigSchema map[string]ConfigSchemaField `yaml:"config_schema"`
}

// LoadManifest reads plugin.yaml from the current working directory (or the
// standard system config path) and returns the parsed manifest.
func LoadManifest() Manifest {
	candidates := []string{
		"plugin.yaml",                                    // dev mode (air, local run)
		"/usr/local/etc/teamagentica/plugin.yaml",        // production containers
	}

	var data []byte
	var err error
	for _, path := range candidates {
		data, err = os.ReadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		log.Fatalf("pluginsdk: failed to load plugin.yaml (tried %v): %v", candidates, err)
	}

	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		log.Fatalf("pluginsdk: failed to parse plugin.yaml: %v", err)
	}
	if m.ID == "" {
		log.Fatalf("pluginsdk: plugin.yaml missing required 'id' field")
	}
	return m
}

// SelectOption represents a select field option with a display label and API value.
// It can be unmarshaled from either a plain string or a {label, value} object.
type SelectOption struct {
	Label string `json:"label" yaml:"label"`
	Value string `json:"value" yaml:"value"`
}

// UnmarshalYAML allows SelectOption to be parsed from a plain string or a map.
func (o *SelectOption) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// Try string first
	var s string
	if err := unmarshal(&s); err == nil {
		o.Label = s
		o.Value = s
		return nil
	}
	// Try map
	type raw struct {
		Label string `yaml:"label"`
		Value string `yaml:"value"`
	}
	var r raw
	if err := unmarshal(&r); err != nil {
		return err
	}
	o.Label = r.Label
	o.Value = r.Value
	return nil
}

// UnmarshalJSON allows SelectOption to be parsed from a plain string or a JSON object.
func (o *SelectOption) UnmarshalJSON(data []byte) error {
	// Try string first
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		o.Label = s
		o.Value = s
		return nil
	}
	// Try object
	type raw struct {
		Label string `json:"label"`
		Value string `json:"value"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	o.Label = r.Label
	o.Value = r.Value
	return nil
}

// ConfigSchemaField describes a single configuration field for a plugin.
type ConfigSchemaField struct {
	Type        string            `json:"type" yaml:"type"`
	Label       string            `json:"label" yaml:"label"`
	Required    bool              `json:"required,omitempty" yaml:"required,omitempty"`
	Secret      bool              `json:"secret,omitempty" yaml:"secret,omitempty"`
	ReadOnly    bool              `json:"readonly,omitempty" yaml:"readonly,omitempty"`
	Default     string            `json:"default,omitempty" yaml:"default,omitempty"`
	Options     []SelectOption    `json:"options,omitempty" yaml:"options,omitempty"`
	Dynamic     bool              `json:"dynamic,omitempty" yaml:"dynamic,omitempty"`
	HelpText    string            `json:"help_text,omitempty" yaml:"help_text,omitempty"`
	VisibleWhen *VisibleWhen      `json:"visible_when,omitempty" yaml:"visible_when,omitempty"`
	Order       int               `json:"order,omitempty" yaml:"order,omitempty"`
}

// VisibleWhen describes a condition under which a field should be visible.
type VisibleWhen struct {
	Field string `json:"field" yaml:"field"`
	Value string `json:"value" yaml:"value"`
}

// SchemaFunc is called on each GET /schema request, allowing plugins to return
// a dynamic schema that reflects current config state. If nil, the static
// Schema/ConfigSchema/WorkspaceSchema fields are used instead.
type SchemaFunc func() map[string]interface{}

// DiscordCommandOption describes a single option/argument for a Discord command or subcommand.
type DiscordCommandOption struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`              // "string", "integer", "boolean"
	Required    bool   `json:"required,omitempty"`
}

// DiscordSubcommand describes a subcommand within a Discord slash command.
type DiscordSubcommand struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Endpoint    string                 `json:"endpoint"` // POST endpoint on this plugin
	Options     []DiscordCommandOption `json:"options,omitempty"`
}

// DiscordCommand describes a slash command a plugin exposes to Discord bots.
// Either Endpoint (leaf command) or Subcommands should be set, not both.
type DiscordCommand struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Endpoint    string                 `json:"endpoint,omitempty"`   // for leaf commands
	Options     []DiscordCommandOption `json:"options,omitempty"`    // for leaf commands
	Subcommands []DiscordSubcommand    `json:"subcommands,omitempty"` // for grouped commands
}

// DiscordCommandResponse is returned by a plugin's discord command endpoint.
type DiscordCommandResponse struct {
	Type    string                 `json:"type"`              // "text" or "embed"
	Content string                 `json:"content,omitempty"` // for type "text"
	Embeds  []DiscordEmbedResponse `json:"embeds,omitempty"`  // for type "embed"
}

// DiscordEmbedResponse describes a single Discord embed.
type DiscordEmbedResponse struct {
	Title  string                    `json:"title,omitempty"`
	Color  int                       `json:"color,omitempty"`
	Fields []DiscordEmbedFieldResponse `json:"fields,omitempty"`
}

// DiscordEmbedFieldResponse describes a single field within a Discord embed.
type DiscordEmbedFieldResponse struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
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
	EventPort    int                          `json:"event_port,omitempty"`
	Capabilities []string                     `json:"capabilities"`
	Version      string                       `json:"version"`
	Candidate    bool                         `json:"candidate,omitempty"` // true if running as a candidate container
	Dependencies    PluginDependencies           `json:"dependencies,omitempty"`
	Schema          map[string]interface{}       `json:"schema,omitempty"`
	ConfigSchema    map[string]ConfigSchemaField `json:"config_schema,omitempty"`
	WorkspaceSchema map[string]interface{}       `json:"workspace_schema,omitempty"`
	DiscordCommands []DiscordCommand             `json:"discord_commands,omitempty"`
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

// Config holds the kernel connection info populated from environment variables.
type Config struct {
	KernelHost string // TEAMAGENTICA_KERNEL_HOST
	KernelPort string // TEAMAGENTICA_KERNEL_PORT
	TLSCert    string // TEAMAGENTICA_TLS_CERT
	TLSKey     string // TEAMAGENTICA_TLS_KEY
	TLSCA      string // TEAMAGENTICA_TLS_CA
	Candidate  bool   // TEAMAGENTICA_CANDIDATE — true if running as a candidate container
}

// LoadConfig reads plugin SDK config from environment variables.
func LoadConfig() Config {
	return Config{
		KernelHost: os.Getenv("TEAMAGENTICA_KERNEL_HOST"),
		KernelPort: os.Getenv("TEAMAGENTICA_KERNEL_PORT"),
		TLSCert:    os.Getenv("TEAMAGENTICA_TLS_CERT"),
		TLSKey:     os.Getenv("TEAMAGENTICA_TLS_KEY"),
		TLSCA:      os.Getenv("TEAMAGENTICA_TLS_CA"),
		Candidate:  os.Getenv("TEAMAGENTICA_CANDIDATE") == "true",
	}
}

// Client manages the plugin's relationship with the kernel.
type Client struct {
	config       Config
	registration Registration
	httpClient   *http.Client
	routeClient  *http.Client // longer timeout for RouteToPlugin (AI chat)
	stopCh       chan struct{}

	// Internal event server for receiving kernel callbacks.
	eventServer     *http.Server
	eventPort       int
	eventDebouncers map[string]Debouncer
	eventMu         sync.RWMutex
	registeredCh    chan struct{} // closed after successful kernel registration

	// Cached storage plugin discovery.
	storagePluginID string
	storageMu       sync.RWMutex
}

// PluginID returns the plugin's registered ID.
func (c *Client) PluginID() string {
	return c.registration.ID
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
// Always starts an internal server on an ephemeral port to serve /schema and
// event callbacks. Includes EventPort in registration for kernel proxying.
// Retries registration with exponential backoff (1s, 2s, 4s, 8s, max 30s).
// This is non-blocking.
func (c *Client) Start(ctx context.Context) {
	// Always start internal server (serves /schema + event callbacks).
	if err := c.startInternalServer(); err != nil {
		log.Printf("pluginsdk: WARNING: failed to start internal server: %v", err)
	} else {
		c.registration.EventPort = c.eventPort
	}

	go func() {
		// Registration with exponential backoff.
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
			break
		}

		// Signal that registration is complete — OnEvent calls after this
		// point will subscribe immediately.
		close(c.registeredCh)

		// Subscribe any event handlers that were registered before Start completed.
		c.eventMu.RLock()
		pending := make([]string, 0, len(c.eventDebouncers))
		for k := range c.eventDebouncers {
			pending = append(pending, k)
		}
		c.eventMu.RUnlock()

		for _, eventType := range pending {
			if err := c.Subscribe(eventType, "/events"); err != nil {
				log.Printf("pluginsdk: failed to subscribe to %s: %v", eventType, err)
			} else {
				log.Printf("pluginsdk: subscribed to %s", eventType)
			}
		}

		// Heartbeat loop every 30 seconds.
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
	}()
}

// ReportEvent sends a debug event to the kernel for display in the console.
func (c *Client) ReportEvent(eventType, detail string) {
	payload := map[string]string{
		"id":     c.registration.ID,
		"type":   eventType,
		"detail": detail,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	req, err := http.NewRequest(http.MethodPost, c.kernelURL()+"/api/plugins/event", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
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

// ReportUsage sends a usage report to the kernel as a usage:report event
// with addressed delivery to infra-cost-tracking for guaranteed at-least-once processing.
func (c *Client) ReportUsage(report UsageReport) {
	data, err := json.Marshal(report)
	if err != nil {
		log.Printf("sdk: ReportUsage marshal error: %v", err)
		return
	}

	payload := map[string]string{
		"id":          c.registration.ID,
		"type":        "usage:report",
		"detail":      string(data),
		"destination": "infra-cost-tracking",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("sdk: ReportUsage payload marshal error: %v", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, c.kernelURL()+"/api/plugins/event", bytes.NewReader(body))
	if err != nil {
		log.Printf("sdk: ReportUsage request error: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("sdk: ReportUsage send error: %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("sdk: ReportUsage got %d from kernel", resp.StatusCode)
	}
}

// ReportAddressedEvent sends an event with addressed delivery to a specific plugin.
// The kernel guarantees at-least-once delivery, queuing if the destination is offline.
func (c *Client) ReportAddressedEvent(eventType, detail, destination string) {
	payload := map[string]string{
		"id":          c.registration.ID,
		"type":        eventType,
		"detail":      detail,
		"destination": destination,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("sdk: ReportAddressedEvent marshal error: %v", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, c.kernelURL()+"/api/plugins/event", bytes.NewReader(body))
	if err != nil {
		log.Printf("sdk: ReportAddressedEvent request error: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("sdk: ReportAddressedEvent send error: %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("sdk: ReportAddressedEvent got %d from kernel", resp.StatusCode)
	}
}

// Subscribe registers interest in events of the given type.
// When such events occur, the kernel will POST to callbackPath on this plugin's HTTP server.
func (c *Client) Subscribe(eventType, callbackPath string) error {
	payload := map[string]string{
		"id":            c.registration.ID,
		"event_type":    eventType,
		"callback_path": callbackPath,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal subscribe: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.kernelURL()+"/api/plugins/subscribe", bytes.NewReader(body))
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

// OnEvent registers a handler for the given event type with a debouncer strategy.
// Can be called before or after Start(). If called after registration has
// completed, subscribes to the kernel immediately. If called before, the
// subscription is deferred until Start() finishes registering.
//
// Use NewNullDebouncer(handler) for immediate delivery of every event.
// Use NewTimedDebouncer(duration, handler) to coalesce rapid events.
func (c *Client) OnEvent(eventType string, debouncer Debouncer) error {
	c.eventMu.Lock()
	if c.eventDebouncers == nil {
		c.eventDebouncers = make(map[string]Debouncer)
	}
	c.eventDebouncers[eventType] = debouncer
	c.eventMu.Unlock()

	// If already registered with kernel, subscribe immediately.
	select {
	case <-c.registeredCh:
		if err := c.Subscribe(eventType, "/events"); err != nil {
			return err
		}
		log.Printf("pluginsdk: subscribed to %s", eventType)
	default:
		// Not yet registered — Start() will subscribe pending handlers.
	}
	return nil
}

// WhenPluginAvailable calls fn when a plugin with the given capability is available.
// It performs an immediate lookup and, if not found, listens for plugin:registered
// events to catch late-starting plugins. fn is also called on re-registration
// (e.g. after a plugin restart). Safe to call before or after Start().
func (c *Client) WhenPluginAvailable(capability string, fn func(PluginInfo)) {
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
	if len(c.registration.DiscordCommands) > 0 {
		schema["discord_commands"] = c.registration.DiscordCommands
	}
	if len(schema) == 0 {
		return nil
	}
	return schema
}

// startInternalServer starts an internal HTTP server on an ephemeral port.
// Serves GET /schema (live plugin schema) and POST /events (kernel callbacks).
func (c *Client) startInternalServer() error {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return fmt.Errorf("listen ephemeral port: %w", err)
	}
	c.eventPort = ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()

	// Always serve schema.
	if c.registration.SchemaFunc != nil {
		// Dynamic schema — call function on each request.
		mux.HandleFunc("GET /schema", func(w http.ResponseWriter, r *http.Request) {
			data := c.registration.SchemaFunc()
			if c.registration.ToolsFunc != nil {
				data["tools"] = c.registration.ToolsFunc()
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(data)
		})
	} else if schemaData := c.buildSchemaJSON(); schemaData != nil {
		// Inject tools if available.
		if c.registration.ToolsFunc != nil {
			schemaData["tools"] = c.registration.ToolsFunc()
		}
		// Static schema — marshal once.
		schemaBytes, err := json.Marshal(schemaData)
		if err != nil {
			log.Printf("pluginsdk: WARNING: failed to marshal schema: %v", err)
		} else {
			mux.HandleFunc("GET /schema", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write(schemaBytes)
			})
		}
	} else if c.registration.ToolsFunc != nil {
		// No schema but tools available — serve tools-only schema.
		schemaData := map[string]interface{}{"tools": c.registration.ToolsFunc()}
		schemaBytes, _ := json.Marshal(schemaData)
		mux.HandleFunc("GET /schema", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(schemaBytes)
		})
	}

	// Event callback handler.
	mux.HandleFunc("POST /events", c.handleEventCallback)

	c.eventServer = &http.Server{Handler: mux}

	// Use TLS on the internal server when mTLS is enabled so the kernel
	// (which always uses pluginScheme → "https") can reach /schema and /events.
	if tlsCfg, err := GetServerTLSConfig(c.config); err != nil {
		log.Printf("pluginsdk: WARNING: failed to configure TLS for internal server: %v — serving plain HTTP", err)
	} else if tlsCfg != nil {
		ln = tls.NewListener(ln, tlsCfg)
		log.Printf("pluginsdk: internal server listening on :%d (schema + events, mTLS)", c.eventPort)
	} else {
		log.Printf("pluginsdk: internal server listening on :%d (schema + events)", c.eventPort)
	}

	go func() {
		if err := c.eventServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("pluginsdk: internal server error: %v", err)
		}
	}()
	return nil
}

// handleEventCallback handles POST /events — dispatches to registered handlers.
func (c *Client) handleEventCallback(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var event EventCallback
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	c.eventMu.RLock()
	debouncer, ok := c.eventDebouncers[event.EventType]
	c.eventMu.RUnlock()

	if ok {
		debouncer.Submit(event)
	} else {
		log.Printf("pluginsdk: no handler for event type %q", event.EventType)
	}

	w.WriteHeader(http.StatusOK)
}

// ── Webhook helpers ──────────────────────────────────────────────────────────

// RegisterWebhook registers this plugin's webhook route with the webhook ingress.
// prefix is the URL prefix the ingress will match (e.g. "/tool-seedance").
// Also subscribes to webhook:ready so the route is re-registered if the ingress restarts.
func (c *Client) RegisterWebhook(prefix string) {
	// Ensure prefix starts with /
	if prefix == "" || prefix[0] != '/' {
		prefix = "/" + prefix
	}

	pluginID := c.registration.ID
	hostname := c.registration.Host
	port := c.registration.Port

	send := func() {
		payload, _ := json.Marshal(map[string]interface{}{
			"plugin_id":   pluginID,
			"prefix":      prefix,
			"target_host": hostname,
			"target_port": port,
		})
		c.ReportAddressedEvent("webhook:api:update", string(payload), "network-webhook")
		log.Printf("pluginsdk: sent webhook route to ingress: prefix=%s target=%s:%d", prefix, hostname, port)
	}

	// Subscribe to webhook:ready so we re-register when the ingress (re)starts.
	c.OnEvent("webhook:ready", NewNullDebouncer(func(event EventCallback) {
		log.Printf("pluginsdk: webhook:ready received — registering route")
		send()
	}))
}

// OnWebhookURL registers a callback that fires when the webhook ingress sends
// this plugin its public webhook URL. The callback receives the full URL
// (e.g. "https://abc.ngrok.io/tool-seedance").
func (c *Client) OnWebhookURL(fn func(webhookURL string)) {
	c.OnEvent("webhook:plugin:url", NewNullDebouncer(func(event EventCallback) {
		var data struct {
			WebhookURL string `json:"webhook_url"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &data); err != nil {
			log.Printf("pluginsdk: failed to parse webhook:plugin:url: %v", err)
			return
		}
		if data.WebhookURL == "" {
			log.Printf("pluginsdk: webhook:plugin:url has empty URL")
			return
		}
		log.Printf("pluginsdk: received webhook URL: %s", data.WebhookURL)
		fn(data.WebhookURL)
	}))
}

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
	c.ReportAddressedEvent("relay:task:progress", string(payload), "infra-agent-relay")
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

/// FetchConfig retrieves the plugin's own configuration from the kernel API.
// Returns a map of config key → value (unmasked, including secrets).
func (c *Client) FetchConfig() (map[string]string, error) {
	url := fmt.Sprintf("%s/api/plugins/%s/self-config", c.kernelURL(), c.registration.ID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("kernel returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Config map[string]string `json:"config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return result.Config, nil
}

// ManagedContainerInfo represents a managed container tracked by the kernel.
type ManagedContainerInfo struct {
	ID       string `json:"id"`
	PluginID string `json:"plugin_id"`
	Name     string `json:"name"`
	Image         string `json:"image"`
	Status        string `json:"status"`
	Port          int    `json:"port"`
	Subdomain     string `json:"subdomain"`
	VolumeName    string `json:"volume_name"`
}

// ExtraMount describes an additional bind mount for a managed container.
type ExtraMount struct {
	VolumeName string `json:"volume_name"`        // volume dir name (same convention as primary VolumeName)
	Target     string `json:"target"`              // mount path inside the container
	ReadOnly   bool   `json:"read_only,omitempty"` // mount as read-only
}

// CreateManagedContainerRequest is the body for creating a managed container.
type CreateManagedContainerRequest struct {
	Name          string            `json:"name"`
	Image         string            `json:"image"`
	Port          int               `json:"port"`
	Subdomain     string            `json:"subdomain"`
	VolumeName  string       `json:"volume_name,omitempty"`
	ExtraMounts []ExtraMount `json:"extra_mounts,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Cmd           []string          `json:"cmd,omitempty"`
	DockerUser    string            `json:"docker_user,omitempty"`
	PluginSource  string            `json:"plugin_source,omitempty"` // plugin name whose source to bind-mount for dev editing
}

// CreateManagedContainer asks the kernel to launch a managed container.
func (c *Client) CreateManagedContainer(req CreateManagedContainerRequest) (*ManagedContainerInfo, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequest(http.MethodPost, c.kernelURL()+"/api/plugins/containers", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("kernel returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var mc ManagedContainerInfo
	if err := json.NewDecoder(resp.Body).Decode(&mc); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &mc, nil
}

// ListManagedContainers returns all managed containers owned by this plugin.
func (c *Client) ListManagedContainers() ([]ManagedContainerInfo, error) {
	req, err := http.NewRequest(http.MethodGet, c.kernelURL()+"/api/plugins/containers", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kernel returned status %d", resp.StatusCode)
	}

	var containers []ManagedContainerInfo
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return containers, nil
}

// StartManagedContainer re-launches a stopped managed container.
func (c *Client) StartManagedContainer(containerID string) (*ManagedContainerInfo, error) {
	req, err := http.NewRequest(http.MethodPost, c.kernelURL()+"/api/plugins/containers/"+containerID+"/start", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("kernel returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var mc ManagedContainerInfo
	if err := json.NewDecoder(resp.Body).Decode(&mc); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &mc, nil
}

// DeleteManagedContainer stops and removes a managed container.
func (c *Client) DeleteManagedContainer(containerID string) error {
	req, err := http.NewRequest(http.MethodDelete, c.kernelURL()+"/api/plugins/containers/"+containerID, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kernel returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// UpdateManagedContainerRequest is the body for patching a managed container.
type UpdateManagedContainerRequest struct {
	Name       *string `json:"name,omitempty"`
	Subdomain  *string `json:"subdomain,omitempty"`
	VolumeName *string `json:"volume_name,omitempty"`
}

// UpdateManagedContainer patches a managed container's metadata.
func (c *Client) UpdateManagedContainer(containerID string, req UpdateManagedContainerRequest) (*ManagedContainerInfo, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequest(http.MethodPatch, c.kernelURL()+"/api/plugins/containers/"+containerID, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("kernel returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var mc ManagedContainerInfo
	if err := json.NewDecoder(resp.Body).Decode(&mc); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &mc, nil
}

// --- Storage helpers (route through kernel to storage:api plugin) ---

// StorageFile holds metadata returned by storage list operations.
type StorageFile struct {
	Key          string `json:"key"`
	Size         int64  `json:"size"`
	ContentType  string `json:"content_type"`
	LastModified string `json:"last_modified"`
	ETag         string `json:"etag"`
}

// StorageListResult holds the response from StorageList.
type StorageListResult struct {
	Objects []StorageFile `json:"objects"`
	Count   int           `json:"count"`
}

// StorageBrowseResult holds the response from StorageBrowse.
type StorageBrowseResult struct {
	Prefix  string        `json:"prefix"`
	Folders []string      `json:"folders"`
	Files   []StorageFile `json:"files"`
}

// resolveStoragePlugin finds the plugin with tool:storage capability.
// Caches the result on the Client instance; retries on failure.
func (c *Client) resolveStoragePlugin() (string, error) {
	c.storageMu.RLock()
	if c.storagePluginID != "" {
		id := c.storagePluginID
		c.storageMu.RUnlock()
		return id, nil
	}
	c.storageMu.RUnlock()

	c.storageMu.Lock()
	defer c.storageMu.Unlock()

	// Double-check after acquiring write lock.
	if c.storagePluginID != "" {
		return c.storagePluginID, nil
	}

	req, err := http.NewRequest(http.MethodGet, c.kernelURL()+"/api/plugins/search?capability=storage:api", nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("kernel returned status %d", resp.StatusCode)
	}

	var result struct {
		Plugins []struct {
			ID string `json:"id"`
		} `json:"plugins"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(result.Plugins) == 0 {
		return "", fmt.Errorf("no storage plugin found")
	}

	c.storagePluginID = result.Plugins[0].ID
	log.Printf("pluginsdk: resolved storage plugin: %s", c.storagePluginID)
	return c.storagePluginID, nil
}

// storageRoute builds the kernel proxy URL for a storage operation.
func (c *Client) storageRoute(path string) (string, error) {
	pluginID, err := c.resolveStoragePlugin()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/api/route/%s%s", c.kernelURL(), pluginID, path), nil
}

// StorageWrite uploads data to the storage plugin.
func (c *Client) StorageWrite(ctx context.Context, key string, data io.Reader, contentType string) error {
	url, err := c.storageRoute("/objects/" + key)
	if err != nil {
		return fmt.Errorf("storage write: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, data)
	if err != nil {
		return fmt.Errorf("storage write: build request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("storage write: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("storage write: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// StorageRead downloads data from the storage plugin.
// Returns the body reader (caller must close), content type, and error.
func (c *Client) StorageRead(ctx context.Context, key string) (io.ReadCloser, string, error) {
	url, err := c.storageRoute("/objects/" + key)
	if err != nil {
		return nil, "", fmt.Errorf("storage read: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("storage read: build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("storage read: %w", err)
	}

	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, "", fmt.Errorf("storage read: status %d", resp.StatusCode)
	}

	return resp.Body, resp.Header.Get("Content-Type"), nil
}

// StorageDelete removes an object from the storage plugin.
func (c *Client) StorageDelete(ctx context.Context, key string) error {
	url, err := c.storageRoute("/objects/" + key)
	if err != nil {
		return fmt.Errorf("storage delete: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("storage delete: build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("storage delete: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("storage delete: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// StorageList returns all objects matching the given prefix.
func (c *Client) StorageList(ctx context.Context, prefix string) (*StorageListResult, error) {
	url, err := c.storageRoute("/list?prefix=" + prefix)
	if err != nil {
		return nil, fmt.Errorf("storage list: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("storage list: build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("storage list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("storage list: status %d", resp.StatusCode)
	}

	var result StorageListResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("storage list: decode: %w", err)
	}
	return &result, nil
}

// --- Plugin discovery and routing helpers ---

// PluginInfo holds plugin metadata returned by SearchPlugins.
type PluginInfo struct {
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	Version         string                 `json:"version"`
	Image           string                 `json:"image"`
	Status          string                 `json:"status"`
	Capabilities    []string               `json:"capabilities"`
	WorkspaceSchema map[string]interface{} `json:"workspace_schema,omitempty"`
}

// SearchPlugins queries the kernel for plugins whose capabilities match the given prefix.
func (c *Client) SearchPlugins(capabilityPrefix string) ([]PluginInfo, error) {
	req, err := http.NewRequest(http.MethodGet, c.kernelURL()+"/api/plugins/search?capability="+capabilityPrefix, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kernel returned status %d", resp.StatusCode)
	}

	var result struct {
		Plugins []PluginInfo `json:"plugins"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result.Plugins, nil
}

// GetPluginSchema fetches the live schema from a plugin via the kernel proxy.
// Returns the full schema map with sections like "config", "workspace", etc.
func (c *Client) GetPluginSchema(pluginID string) (map[string]interface{}, error) {
	url := fmt.Sprintf("%s/api/plugins/%s/schema", c.kernelURL(), pluginID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kernel returned status %d", resp.StatusCode)
	}

	var schema map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&schema); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return schema, nil
}

// RouteToPlugin proxies a request through the kernel to a specific plugin.
// Returns the raw response body. The caller is responsible for interpreting it.
func (c *Client) RouteToPlugin(ctx context.Context, pluginID, method, path string, body io.Reader) ([]byte, error) {
	url := fmt.Sprintf("%s/api/route/%s%s", c.kernelURL(), pluginID, path)

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.routeClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("plugin %s returned status %d: %s", pluginID, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// RouteToPluginWithHeaders is like RouteToPlugin but allows setting custom headers on the request.
func (c *Client) RouteToPluginWithHeaders(ctx context.Context, pluginID, method, path string, body io.Reader, headers map[string]string) ([]byte, error) {
	url := fmt.Sprintf("%s/api/route/%s%s", c.kernelURL(), pluginID, path)

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.routeClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("plugin %s returned status %d: %s", pluginID, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// RouteToPluginStream opens a streaming connection to a plugin endpoint and returns
// the raw HTTP response. The caller owns the response body and must close it.
// Unlike RouteToPlugin, this uses no timeout — the caller controls lifetime via ctx.
func (c *Client) RouteToPluginStream(ctx context.Context, pluginID, method, path string, body io.Reader) (*http.Response, error) {
	url := fmt.Sprintf("%s/api/route/%s%s", c.kernelURL(), pluginID, path)

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "text/event-stream")

	// Use a client without timeout for streaming — context cancellation handles cleanup.
	streamClient := &http.Client{Transport: c.routeClient.Transport}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("plugin %s returned status %d: %s", pluginID, resp.StatusCode, string(respBody))
	}

	return resp, nil
}

// DeployCandidate tells the kernel to start a candidate container for the given plugin.
func (c *Client) DeployCandidate(ctx context.Context, pluginID string, image string) error {
	payload := map[string]interface{}{"image": image}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/plugins/deploy/%s", c.kernelURL(), pluginID),
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("deploy returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// PromoteCandidate tells the kernel to promote a candidate to primary.
func (c *Client) PromoteCandidate(ctx context.Context, pluginID string) error {
	return c.pluginAction(ctx, pluginID, "promote")
}

// RollbackCandidate tells the kernel to stop a candidate and revert to primary.
func (c *Client) RollbackCandidate(ctx context.Context, pluginID string) error {
	return c.pluginAction(ctx, pluginID, "rollback")
}

func (c *Client) pluginAction(ctx context.Context, pluginID, action string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/plugins/%s/%s", c.kernelURL(), action, pluginID),
		bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s returned %d: %s", action, resp.StatusCode, string(respBody))
	}
	return nil
}

// Stop deregisters from the kernel, shuts down the event server, and stops the heartbeat loop.
func (c *Client) Stop() {
	// Stop all debouncers.
	c.eventMu.RLock()
	for _, d := range c.eventDebouncers {
		d.Stop()
	}
	c.eventMu.RUnlock()

	// Shutdown event server.
	if c.eventServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := c.eventServer.Shutdown(ctx); err != nil {
			log.Printf("pluginsdk: event server shutdown error: %v", err)
		}
	}

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

// buildClientTLSConfig creates a tls.Config for outbound mTLS connections.
func buildClientTLSConfig(certPath, keyPath, caPath string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load client cert: %w", err)
	}

	caCert, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to add CA cert to pool")
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// GetServerTLSConfig returns a tls.Config for a plugin's HTTPS server.
// Requires client certs from the CA for mutual authentication.
// Returns nil if TLS is not enabled.
func GetServerTLSConfig(cfg Config) (*tls.Config, error) {
	if cfg.TLSCert == "" || cfg.TLSKey == "" || cfg.TLSCA == "" {
		return nil, nil
	}

	cert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
	if err != nil {
		return nil, fmt.Errorf("load server cert: %w", err)
	}

	caCert, err := os.ReadFile(cfg.TLSCA)
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to add CA cert to pool")
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}, nil
}
