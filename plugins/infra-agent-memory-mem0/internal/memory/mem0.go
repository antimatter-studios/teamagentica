package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Mem0Provider implements Provider by calling the local Mem0 REST API.
type Mem0Provider struct {
	baseURL string
	client  *http.Client
}

// NewMem0Provider creates a provider that talks to a Mem0 server at the given base URL.
func NewMem0Provider(baseURL string) *Mem0Provider {
	return &Mem0Provider{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 120 * time.Second, // extraction can be slow
		},
	}
}

// Add sends messages to Mem0 for fact extraction and storage.
func (m *Mem0Provider) Add(ctx context.Context, messages []Message, opts AddOpts) ([]Memory, error) {
	body := map[string]any{
		"messages": messages,
	}
	if opts.UserID != "" {
		body["user_id"] = opts.UserID
	}
	if opts.AgentID != "" {
		body["agent_id"] = opts.AgentID
	}
	if opts.AppID != "" {
		body["app_id"] = opts.AppID
	}
	if opts.RunID != "" {
		body["run_id"] = opts.RunID
	}
	if opts.Metadata != nil {
		body["metadata"] = opts.Metadata
	}
	if opts.Infer != nil {
		body["infer"] = *opts.Infer
	}
	if opts.Immutable != nil {
		body["immutable"] = *opts.Immutable
	}
	if opts.EnableGraph != nil {
		body["enable_graph"] = *opts.EnableGraph
	}
	if opts.ExpirationDate != "" {
		body["expiration_date"] = opts.ExpirationDate
	}
	if len(opts.CustomCategories) > 0 {
		body["custom_categories"] = opts.CustomCategories
	}
	if opts.CustomInstructions != "" {
		body["custom_instructions"] = opts.CustomInstructions
	}

	var resp struct {
		Results []Memory `json:"results"`
	}
	if err := m.doJSON(ctx, "POST", "/v1/memories/", body, &resp); err != nil {
		return nil, fmt.Errorf("add memories: %w", err)
	}
	return resp.Results, nil
}

// Search performs semantic search across stored memories.
func (m *Mem0Provider) Search(ctx context.Context, query string, opts SearchOpts) ([]Memory, error) {
	body := map[string]any{
		"query": query,
	}
	if opts.Filters != nil {
		for k, v := range opts.Filters {
			body[k] = v
		}
	}
	if opts.TopK > 0 {
		body["top_k"] = opts.TopK
	}
	if opts.Threshold > 0 {
		body["threshold"] = opts.Threshold
	}
	if opts.Rerank != nil {
		body["rerank"] = *opts.Rerank
	}
	if opts.KeywordSearch != nil {
		body["keyword_search"] = *opts.KeywordSearch
	}

	var resp struct {
		Results []Memory `json:"results"`
	}
	if err := m.doJSON(ctx, "POST", "/v1/memories/search/", body, &resp); err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}
	return resp.Results, nil
}

// List returns memories matching the given filters with pagination.
func (m *Mem0Provider) List(ctx context.Context, opts ListOpts) ([]Memory, error) {
	q := url.Values{}
	if opts.Filters != nil {
		if v, ok := opts.Filters["user_id"].(string); ok && v != "" {
			q.Set("user_id", v)
		}
		if v, ok := opts.Filters["agent_id"].(string); ok && v != "" {
			q.Set("agent_id", v)
		}
		if v, ok := opts.Filters["app_id"].(string); ok && v != "" {
			q.Set("app_id", v)
		}
		if v, ok := opts.Filters["run_id"].(string); ok && v != "" {
			q.Set("run_id", v)
		}
	}
	if opts.Page > 0 {
		q.Set("page", fmt.Sprintf("%d", opts.Page))
	}
	if opts.PageSize > 0 {
		q.Set("page_size", fmt.Sprintf("%d", opts.PageSize))
	}

	path := "/v1/memories/"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	// Mem0 returns a raw JSON array, not {"results": [...]}.
	var memories []Memory
	if err := m.doJSON(ctx, "GET", path, nil, &memories); err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	return memories, nil
}

// Count returns the total number of memories matching the given filters.
func (m *Mem0Provider) Count(ctx context.Context, filters map[string]any) (int, error) {
	q := url.Values{}
	if filters != nil {
		if v, ok := filters["user_id"].(string); ok && v != "" {
			q.Set("user_id", v)
		}
		if v, ok := filters["agent_id"].(string); ok && v != "" {
			q.Set("agent_id", v)
		}
		if v, ok := filters["run_id"].(string); ok && v != "" {
			q.Set("run_id", v)
		}
	}

	path := "/v1/memories/count"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var resp struct {
		Total int `json:"total"`
	}
	if err := m.doJSON(ctx, "GET", path, nil, &resp); err != nil {
		return 0, fmt.Errorf("count memories: %w", err)
	}
	return resp.Total, nil
}

// Get retrieves a single memory by ID.
func (m *Mem0Provider) Get(ctx context.Context, memoryID string) (*Memory, error) {
	var mem Memory
	if err := m.doJSON(ctx, "GET", "/v1/memories/"+memoryID+"/", nil, &mem); err != nil {
		return nil, fmt.Errorf("get memory %s: %w", memoryID, err)
	}
	return &mem, nil
}

// Update modifies a memory's text and/or metadata.
func (m *Mem0Provider) Update(ctx context.Context, memoryID string, text string, metadata map[string]any) error {
	body := map[string]any{}
	if text != "" {
		body["text"] = text
	}
	if metadata != nil {
		body["metadata"] = metadata
	}
	if err := m.doJSON(ctx, "PUT", "/v1/memories/"+memoryID+"/", body, nil); err != nil {
		return fmt.Errorf("update memory %s: %w", memoryID, err)
	}
	return nil
}

// Delete removes a single memory by ID.
func (m *Mem0Provider) Delete(ctx context.Context, memoryID string) error {
	if err := m.doJSON(ctx, "DELETE", "/v1/memories/"+memoryID+"/", nil, nil); err != nil {
		return fmt.Errorf("delete memory %s: %w", memoryID, err)
	}
	return nil
}

// DeleteAll removes all memories matching the given scope filters.
func (m *Mem0Provider) DeleteAll(ctx context.Context, filters map[string]any) error {
	if err := m.doJSON(ctx, "DELETE", "/v1/memories/", filters, nil); err != nil {
		return fmt.Errorf("delete all memories: %w", err)
	}
	return nil
}

// DeleteEntities hard-deletes an entity and all its associated memories.
func (m *Mem0Provider) DeleteEntities(ctx context.Context, entityType, entityID string) error {
	path := fmt.Sprintf("/v1/entities/%s/%s/", entityType, entityID)
	if err := m.doJSON(ctx, "DELETE", path, nil, nil); err != nil {
		return fmt.Errorf("delete entity %s/%s: %w", entityType, entityID, err)
	}
	return nil
}

// ListEntities enumerates known users, agents, apps, and runs.
func (m *Mem0Provider) ListEntities(ctx context.Context) ([]Entity, error) {
	var resp struct {
		Results []Entity `json:"results"`
	}
	if err := m.doJSON(ctx, "GET", "/v1/entities/", nil, &resp); err != nil {
		return nil, fmt.Errorf("list entities: %w", err)
	}
	return resp.Results, nil
}

// Healthy returns nil if the Mem0 server is reachable.
func (m *Mem0Provider) Healthy(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", m.baseURL+"/", nil)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("mem0 unhealthy: status %d", resp.StatusCode)
	}
	return nil
}

// doJSON is a helper that marshals a request body, sends an HTTP request, and
// decodes the JSON response into dst (if non-nil).
func (m *Mem0Provider) doJSON(ctx context.Context, method, path string, body any, dst any) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, m.baseURL+path, reqBody)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("mem0 %s %s: status %d: %s", method, path, resp.StatusCode, string(respData))
	}

	if dst != nil && len(respData) > 0 {
		if err := json.Unmarshal(respData, dst); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
