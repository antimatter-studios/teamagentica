package pluginsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// peerEntry holds a cached plugin address for direct P2P communication.
type peerEntry struct {
	Host     string
	HTTPPort int
}

// resolvePeer returns the cached address for a plugin, or resolves it from
// the peer cache. Returns false if the plugin is not in the registry.
func (c *Client) resolvePeer(pluginID string) (peerEntry, bool) {
	c.peersMu.RLock()
	entry, ok := c.peers[pluginID]
	c.peersMu.RUnlock()
	return entry, ok
}

// invalidatePeer removes a plugin from the peer cache (e.g. on connection failure
// or plugin:stopped lifecycle event).
func (c *Client) invalidatePeer(pluginID string) {
	c.peersMu.Lock()
	delete(c.peers, pluginID)
	c.peersMu.Unlock()
}

// SetPeer injects a plugin address directly into the peer registry.
// Use this to pre-populate addresses that are known at startup (e.g. from
// the kernel's plugin database) without relying on registry sync events.
func (c *Client) SetPeer(pluginID, host string, httpPort int) {
	c.peersMu.Lock()
	c.peers[pluginID] = peerEntry{Host: host, HTTPPort: httpPort}
	c.peersMu.Unlock()
}

// loadPeerRegistry bulk-loads all running plugin addresses from the kernel.
// Called on startup to pre-populate the cache.
func (c *Client) loadPeerRegistry() {
	req, err := http.NewRequest(http.MethodGet, c.kernelURL()+"/api/plugins/registry", nil)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return
	}

	var result struct {
		Plugins []struct {
			ID       string `json:"id"`
			Host     string `json:"host"`
			HTTPPort int    `json:"http_port"`
		} `json:"plugins"`
	}
	if json.NewDecoder(resp.Body).Decode(&result) != nil {
		return
	}

	c.peersMu.Lock()
	for _, p := range result.Plugins {
		if p.Host != "" {
			c.peers[p.ID] = peerEntry{Host: p.Host, HTTPPort: p.HTTPPort}
		}
	}
	c.peersMu.Unlock()

	if len(result.Plugins) > 0 {
		log.Printf("pluginsdk: loaded %d peer addresses from registry", len(result.Plugins))
	}
}

// RouteHTTPClient returns the mTLS-configured HTTP client used for plugin routing.
// Useful for callers that need to make custom HTTP requests with full header control.
func (c *Client) RouteHTTPClient() *http.Client {
	return c.routeClient
}

// ResolvePeerURL resolves a plugin ID to a direct URL for the given path.
// Returns empty string if the peer cannot be resolved.
func (c *Client) ResolvePeerURL(pluginID, path string) string {
	if entry, ok := c.resolvePeer(pluginID); ok {
		return c.peerURL(entry, path)
	}
	return ""
}

// peerURL builds a direct URL to a peer plugin.
func (c *Client) peerURL(entry peerEntry, path string) string {
	scheme := "http"
	if c.config.TLSCert != "" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%d%s", scheme, entry.Host, entry.HTTPPort, path)
}

// callPeerDirect makes a direct HTTP call to a peer plugin, bypassing the kernel.
func (c *Client) callPeerDirect(ctx context.Context, entry peerEntry, method, path string, body io.Reader, headers map[string]string) ([]byte, error) {
	url := c.peerURL(entry, path)

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
		return nil, err // return unwrapped so caller can detect connection failure
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("plugin returned status %d: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// RouteToPlugin calls a plugin endpoint. Tries direct P2P first using the peer
// registry cache, falls back to kernel proxy on connection failure.
func (c *Client) RouteToPlugin(ctx context.Context, pluginID, method, path string, body io.Reader) ([]byte, error) {
	return c.routeToPluginInternal(ctx, pluginID, method, path, body, nil)
}

// RouteToPluginWithHeaders is like RouteToPlugin but allows setting custom headers.
func (c *Client) RouteToPluginWithHeaders(ctx context.Context, pluginID, method, path string, body io.Reader, headers map[string]string) ([]byte, error) {
	return c.routeToPluginInternal(ctx, pluginID, method, path, body, headers)
}

// routeToPluginInternal routes a request to a plugin via the peer registry.
// Returns an error if the plugin is not in the peer registry.
func (c *Client) routeToPluginInternal(ctx context.Context, pluginID, method, path string, body io.Reader, headers map[string]string) ([]byte, error) {
	entry, ok := c.resolvePeer(pluginID)
	if !ok {
		return nil, fmt.Errorf("plugin %s not found in peer registry", pluginID)
	}

	return c.callPeerDirect(ctx, entry, method, path, body, headers)
}

// RouteToPluginStream opens a streaming connection to a plugin endpoint and returns
// the raw HTTP response. The caller owns the response body and must close it.
// Unlike RouteToPlugin, this uses no timeout — the caller controls lifetime via ctx.
func (c *Client) RouteToPluginStream(ctx context.Context, pluginID, method, path string, body io.Reader) (*http.Response, error) {
	// Try direct P2P for streaming too.
	if entry, ok := c.resolvePeer(pluginID); ok {
		url := c.peerURL(entry, path)
		req, err := http.NewRequestWithContext(ctx, method, url, body)
		if err == nil {
			if body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			streamClient := &http.Client{Transport: c.routeClient.Transport}
			resp, err := streamClient.Do(req)
			if err == nil && resp.StatusCode < 400 {
				return resp, nil
			}
			if err == nil {
				resp.Body.Close()
			}
			// Direct failed — fall through to kernel proxy.
			c.invalidatePeer(pluginID)
		}
	}

	// Fallback: stream via kernel proxy.
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

