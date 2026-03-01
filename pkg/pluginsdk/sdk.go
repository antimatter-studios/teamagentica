package pluginsdk

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"time"
)

// Registration holds the plugin's self-description sent to the kernel on boot.
type Registration struct {
	ID           string   `json:"id"`
	Host         string   `json:"host"`
	Port         int      `json:"port"`
	Capabilities []string `json:"capabilities"`
	Version      string   `json:"version"`
}

// Config holds the kernel connection info populated from environment variables.
type Config struct {
	KernelHost  string // ROBOSLOP_KERNEL_HOST
	KernelPort  string // ROBOSLOP_KERNEL_PORT
	PluginID    string // ROBOSLOP_PLUGIN_ID
	PluginToken string // ROBOSLOP_PLUGIN_TOKEN (service token for auth)
}

// LoadConfig reads plugin SDK config from environment variables.
func LoadConfig() Config {
	return Config{
		KernelHost:  os.Getenv("ROBOSLOP_KERNEL_HOST"),
		KernelPort:  os.Getenv("ROBOSLOP_KERNEL_PORT"),
		PluginID:    os.Getenv("ROBOSLOP_PLUGIN_ID"),
		PluginToken: os.Getenv("ROBOSLOP_PLUGIN_TOKEN"),
	}
}

// Client manages the plugin's relationship with the kernel.
type Client struct {
	config       Config
	registration Registration
	httpClient   *http.Client
	stopCh       chan struct{}
}

// NewClient creates a new SDK client.
func NewClient(cfg Config, reg Registration) *Client {
	return &Client{
		config:       cfg,
		registration: reg,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		stopCh:       make(chan struct{}),
	}
}

// kernelURL returns the base URL for the kernel API.
func (c *Client) kernelURL() string {
	return fmt.Sprintf("http://%s:%s", c.config.KernelHost, c.config.KernelPort)
}

// Start registers with the kernel and begins the heartbeat loop.
// Retries registration with exponential backoff (1s, 2s, 4s, 8s, max 30s)
// until the kernel responds. After successful registration, starts a heartbeat
// goroutine (every 30s). This is non-blocking.
func (c *Client) Start(ctx context.Context) {
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

// Stop deregisters from the kernel and stops the heartbeat loop.
func (c *Client) Stop() {
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
