package pluginsdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
)

// PricingEntry holds pricing data for a single model.
type PricingEntry struct {
	Provider    string  `json:"provider"`
	Model       string  `json:"model"`
	InputPer1M  float64 `json:"input_per_1m"`
	OutputPer1M float64 `json:"output_per_1m"`
	CachedPer1M float64 `json:"cached_per_1m"`
	PerRequest  float64 `json:"per_request"`
	Currency    string  `json:"currency"`
}

// PricingHandler provides GET/PUT /pricing endpoints for plugins.
// Uses stdlib net/http handlers. Wrap with gin.WrapF or gin.WrapH in plugins.
type PricingHandler struct {
	defaults []PricingEntry
	current  atomic.Pointer[[]PricingEntry]
	client   *Client
}

// NewPricingHandler creates a handler with the given default prices.
// The SDK client is used to push price updates to the kernel.
func NewPricingHandler(defaults []PricingEntry, client *Client) *PricingHandler {
	ph := &PricingHandler{
		defaults: defaults,
		client:   client,
	}
	if defaults == nil {
		defaults = []PricingEntry{}
	}
	cur := make([]PricingEntry, len(defaults))
	copy(cur, defaults)
	ph.current.Store(&cur)
	return ph
}

// HandleGet returns the current prices for this plugin's models.
func (p *PricingHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	cur := p.current.Load()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"prices": *cur})
}

// HandlePut accepts updated prices from the user, stores them locally,
// and pushes to the kernel.
func (p *PricingHandler) HandlePut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prices []PricingEntry `json:"prices"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Prices == nil {
		http.Error(w, `{"error":"prices required"}`, http.StatusBadRequest)
		return
	}

	// Update local state.
	p.current.Store(&req.Prices)

	// Push to kernel in background.
	go func() {
		if err := p.pushToKernel(req.Prices); err != nil {
			log.Printf("pricing: failed to push to kernel: %v", err)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"message": "prices updated", "prices": req.Prices})
}

// pushToKernel sends price updates to the kernel's plugin pricing endpoint.
func (p *PricingHandler) pushToKernel(prices []PricingEntry) error {
	payload := map[string]interface{}{
		"prices": prices,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal pricing: %w", err)
	}

	url := p.client.kernelURL() + "/api/plugins/pricing"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.client.config.PluginToken)

	resp, err := p.client.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("kernel returned status %d", resp.StatusCode)
	}
	return nil
}
