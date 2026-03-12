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

// ConfigSchemaField describes a single configuration field for a plugin.
type ConfigSchemaField struct {
	Type        string            `json:"type"`
	Label       string            `json:"label"`
	Required    bool              `json:"required,omitempty"`
	Secret      bool              `json:"secret,omitempty"`
	ReadOnly    bool              `json:"readonly,omitempty"`
	Default     string            `json:"default,omitempty"`
	Options     []string          `json:"options,omitempty"`
	Dynamic     bool              `json:"dynamic,omitempty"`
	HelpText    string            `json:"help_text,omitempty"`
	VisibleWhen *VisibleWhen      `json:"visible_when,omitempty"`
	Order       int               `json:"order,omitempty"`
}

// VisibleWhen describes a condition under which a field should be visible.
type VisibleWhen struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// SchemaFunc is called on each GET /schema request, allowing plugins to return
// a dynamic schema that reflects current config state. If nil, the static
// Schema/ConfigSchema/WorkspaceSchema fields are used instead.
type SchemaFunc func() map[string]interface{}

// Registration holds the plugin's self-description sent to the kernel on boot.
type Registration struct {
	ID           string                       `json:"id"`
	Name         string                       `json:"name,omitempty"`
	Host         string                       `json:"host"`
	Port         int                          `json:"port"`
	EventPort    int                          `json:"event_port,omitempty"`
	Capabilities []string                     `json:"capabilities"`
	Version      string                       `json:"version"`
	Schema          map[string]interface{}       `json:"schema,omitempty"`
	ConfigSchema    map[string]ConfigSchemaField `json:"config_schema,omitempty"`
	WorkspaceSchema map[string]interface{}       `json:"workspace_schema,omitempty"`
	SchemaFunc      SchemaFunc                   `json:"-"`
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
	KernelHost  string // TEAMAGENTICA_KERNEL_HOST
	KernelPort  string // TEAMAGENTICA_KERNEL_PORT
	PluginID    string // TEAMAGENTICA_PLUGIN_ID
	PluginToken string // TEAMAGENTICA_PLUGIN_TOKEN (service token for auth)
	TLSCert     string // TEAMAGENTICA_TLS_CERT
	TLSKey      string // TEAMAGENTICA_TLS_KEY
	TLSCA       string // TEAMAGENTICA_TLS_CA
	TLSEnabled  bool   // TEAMAGENTICA_TLS_ENABLED
}

// LoadConfig reads plugin SDK config from environment variables.
func LoadConfig() Config {
	return Config{
		KernelHost:  os.Getenv("TEAMAGENTICA_KERNEL_HOST"),
		KernelPort:  os.Getenv("TEAMAGENTICA_KERNEL_PORT"),
		PluginID:    os.Getenv("TEAMAGENTICA_PLUGIN_ID"),
		PluginToken: os.Getenv("TEAMAGENTICA_PLUGIN_TOKEN"),
		TLSCert:     os.Getenv("TEAMAGENTICA_TLS_CERT"),
		TLSKey:      os.Getenv("TEAMAGENTICA_TLS_KEY"),
		TLSCA:       os.Getenv("TEAMAGENTICA_TLS_CA"),
		TLSEnabled:  os.Getenv("TEAMAGENTICA_TLS_ENABLED") == "true",
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

	// Cached storage plugin discovery.
	storagePluginID string
	storageMu       sync.RWMutex
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

	if cfg.TLSEnabled && cfg.TLSCert != "" && cfg.TLSKey != "" && cfg.TLSCA != "" {
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

	return &Client{
		config:       cfg,
		registration: reg,
		httpClient:   httpClient,
		routeClient:  routeClient,
		stopCh:       make(chan struct{}),
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
	if c.config.TLSEnabled {
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

		// Subscribe to kernel events for each registered handler.
		c.eventMu.RLock()
		debouncers := make(map[string]Debouncer, len(c.eventDebouncers))
		for k, v := range c.eventDebouncers {
			debouncers[k] = v
		}
		c.eventMu.RUnlock()

		for eventType := range debouncers {
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
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
// with addressed delivery to cost-explorer for guaranteed at-least-once processing.
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
		"destination": "cost-explorer",
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
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
// Must be called before Start(). The SDK will automatically start an internal
// event server and subscribe to the kernel for each registered event type.
//
// Use NewNullDebouncer(handler) for immediate delivery of every event.
// Use NewTimedDebouncer(duration, handler) to coalesce rapid events.
func (c *Client) OnEvent(eventType string, debouncer Debouncer) {
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	if c.eventDebouncers == nil {
		c.eventDebouncers = make(map[string]Debouncer)
	}
	c.eventDebouncers[eventType] = debouncer
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
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(data)
		})
	} else if schemaData := c.buildSchemaJSON(); schemaData != nil {
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
	}

	// Event callback handler.
	mux.HandleFunc("POST /events", c.handleEventCallback)

	c.eventServer = &http.Server{Handler: mux}
	go func() {
		if err := c.eventServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("pluginsdk: internal server error: %v", err)
		}
	}()

	log.Printf("pluginsdk: internal server listening on :%d (schema + events)", c.eventPort)
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

// FetchAliases retrieves the current alias list from the kernel.
// Returns entries in "name=target" format suitable for alias.NewAliasMap or alias.Replace.
func (c *Client) FetchAliases() ([]alias.AliasInfo, error) {
	req, err := http.NewRequest(http.MethodGet, c.kernelURL()+"/api/aliases", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kernel returned status %d", resp.StatusCode)
	}

	var result struct {
		Aliases []alias.AliasInfo `json:"aliases"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return result.Aliases, nil
}

/// FetchConfig retrieves the plugin's own configuration from the kernel API.
// Returns a map of config key → value (unmasked, including secrets).
func (c *Client) FetchConfig() (map[string]string, error) {
	url := fmt.Sprintf("%s/api/plugins/%s/self-config", c.kernelURL(), c.registration.ID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
}

// CreateManagedContainer asks the kernel to launch a managed container.
func (c *Client) CreateManagedContainer(req CreateManagedContainerRequest) (*ManagedContainerInfo, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequest(http.MethodPost, c.kernelURL()+"/api/plugins/containers", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.config.PluginToken)
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
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
	httpReq.Header.Set("Authorization", "Bearer "+c.config.PluginToken)
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
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)
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
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
func (c *Client) heartbeat() error {
	body, err := json.Marshal(map[string]string{"id": c.registration.ID})
	if err != nil {
		return fmt.Errorf("marshal heartbeat: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.kernelURL()+"/api/plugins/heartbeat", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
	req.Header.Set("Authorization", "Bearer "+c.config.PluginToken)

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
	if !cfg.TLSEnabled || cfg.TLSCert == "" || cfg.TLSKey == "" || cfg.TLSCA == "" {
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
