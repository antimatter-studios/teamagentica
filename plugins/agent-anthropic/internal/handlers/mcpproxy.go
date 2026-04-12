package handlers

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"strings"
)

// hopByHopHeaders are per-connection headers that must not be forwarded by a proxy.
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Transfer-Encoding":   true,
	"Te":                  true,
	"Trailer":             true,
	"Upgrade":             true,
	"Proxy-Authorization": true,
	"Proxy-Authenticate":  true,
}

func isHopByHop(header string) bool {
	return hopByHopHeaders[http.CanonicalHeaderKey(header)]
}

func isSSE(resp *http.Response) bool {
	return strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream")
}

// MCPProxyRaw forwards MCP protocol requests to infra-mcp-server via direct
// mTLS HTTP calls. Forwards all headers (stripping hop-by-hop) and streams
// SSE responses for the GET path.
func (h *Handler) MCPProxyRaw(w http.ResponseWriter, r *http.Request) {
	if h.sdk == nil {
		http.Error(w, `{"error":"SDK not initialized"}`, http.StatusServiceUnavailable)
		return
	}

	h.mu.RLock()
	mcpPluginID := h.mcpPluginID
	debug := h.debug
	h.mu.RUnlock()

	if mcpPluginID == "" {
		http.Error(w, `{"error":"MCP server not discovered"}`, http.StatusServiceUnavailable)
		return
	}

	// Resolve the MCP server's direct URL.
	targetURL := h.sdk.ResolvePeerURL(mcpPluginID, "/mcp")
	if targetURL == "" {
		http.Error(w, `{"error":"MCP server peer not resolvable"}`, http.StatusBadGateway)
		return
	}

	// Read the request body.
	var bodyData []byte
	if r.Body != nil {
		var err error
		bodyData, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
			return
		}
		if debug && len(bodyData) > 0 {
			log.Printf("[mcp-proxy] %s /mcp body=%s", r.Method, string(bodyData))
		}
	}

	// Build the upstream request.
	var body io.Reader
	if len(bodyData) > 0 {
		body = bytes.NewReader(bodyData)
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, body)
	if err != nil {
		http.Error(w, `{"error":"failed to create upstream request"}`, http.StatusInternalServerError)
		return
	}

	// Forward ALL request headers except hop-by-hop.
	for key, values := range r.Header {
		if isHopByHop(key) {
			continue
		}
		for _, v := range values {
			req.Header.Add(key, v)
		}
	}
	if req.Header.Get("Content-Type") == "" && len(bodyData) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	// Execute via the SDK's mTLS-configured HTTP client.
	resp, err := h.sdk.RouteHTTPClient().Do(req)
	if err != nil {
		log.Printf("[mcp-proxy] upstream error: %v", err)
		http.Error(w, `{"error":"MCP server unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Forward ALL response headers except hop-by-hop.
	for key, values := range resp.Header {
		if isHopByHop(key) {
			continue
		}
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}

	w.WriteHeader(resp.StatusCode)

	// Stream the response body. Critical for the GET/SSE path where the upstream
	// holds the connection open and sends events incrementally.
	flusher, canFlush := w.(http.Flusher)
	if canFlush && isSSE(resp) {
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
		if debug {
			respBody, _ := io.ReadAll(resp.Body)
			if len(respBody) > 0 {
				log.Printf("[mcp-proxy] %d response=%s sessionId=%s", resp.StatusCode, string(respBody), resp.Header.Get("Mcp-Session-Id"))
			}
			w.Write(respBody)
		} else {
			io.Copy(w, resp.Body)
		}
	}
}

// SetMCPPluginID stores the plugin ID of the MCP server for proxy routing.
func (h *Handler) SetMCPPluginID(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.mcpPluginID = id
}
