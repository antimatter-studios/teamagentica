package pluginsdk

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
)

// MCPBridge is a localhost-only plain HTTP server that bridges requests
// to an mTLS-protected MCP server. Clients without client certificates
// (e.g. Codex CLI) connect here; the proxy forwards via the SDK's mTLS client.
type MCPBridge struct {
	listener net.Listener
	server   *http.Server
	URL      string // e.g. "http://127.0.0.1:38291"
}

// Stop shuts down the MCP TLS proxy.
func (p *MCPBridge) Stop() {
	if p.server != nil {
		p.server.Close()
	}
}

// StartMCPBridge starts a localhost-only plain HTTP proxy that forwards
// all requests to the given MCP plugin's /mcp endpoint via mTLS.
// Returns the proxy with its URL, ready for configuring tools like Codex CLI.
// The proxy resolves the target peer on each request, so it handles restarts.
func (c *Client) StartMCPBridge(mcpPluginID string) (*MCPBridge, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		targetURL := c.ResolvePeerURL(mcpPluginID, "/mcp")
		if targetURL == "" {
			http.Error(w, `{"error":"MCP server not resolvable"}`, http.StatusBadGateway)
			return
		}

		var body io.Reader
		if r.Body != nil {
			bodyData, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
				return
			}
			if len(bodyData) > 0 {
				body = bytes.NewReader(bodyData)
			}
		}

		req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, body)
		if err != nil {
			http.Error(w, `{"error":"failed to create upstream request"}`, http.StatusInternalServerError)
			return
		}

		// Forward headers (skip hop-by-hop).
		hopByHop := map[string]bool{
			"Connection": true, "Keep-Alive": true, "Transfer-Encoding": true,
			"Te": true, "Trailer": true, "Upgrade": true,
			"Proxy-Authorization": true, "Proxy-Authenticate": true,
		}
		for key, values := range r.Header {
			if hopByHop[http.CanonicalHeaderKey(key)] {
				continue
			}
			for _, v := range values {
				req.Header.Add(key, v)
			}
		}

		resp, err := c.routeClient.Do(req)
		if err != nil {
			log.Printf("[mcp-proxy] upstream error: %v", err)
			http.Error(w, `{"error":"MCP server unreachable"}`, http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// Forward response headers.
		for key, values := range resp.Header {
			if hopByHop[http.CanonicalHeaderKey(key)] {
				continue
			}
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		// Stream SSE responses; copy everything else.
		flusher, canFlush := w.(http.Flusher)
		if canFlush && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			buf := make([]byte, 4096)
			for {
				n, readErr := resp.Body.Read(buf)
				if n > 0 {
					w.Write(buf[:n])
					flusher.Flush()
				}
				if readErr != nil {
					break
				}
			}
		} else {
			io.Copy(w, resp.Body)
		}
	})

	server := &http.Server{Handler: mux}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("[mcp-proxy] server error: %v", err)
		}
	}()

	log.Printf("[mcp-proxy] listening on %s → %s/mcp", proxyURL, mcpPluginID)

	return &MCPBridge{
		listener: listener,
		server:   server,
		URL:      proxyURL,
	}, nil
}
