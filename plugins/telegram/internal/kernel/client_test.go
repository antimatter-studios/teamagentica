package kernel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewClient(t *testing.T) {
	c := NewClient("http://localhost:8080", "test-token", false)
	if c.baseURL != "http://localhost:8080" {
		t.Errorf("expected baseURL=http://localhost:8080, got %q", c.baseURL)
	}
	if c.serviceToken != "test-token" {
		t.Errorf("expected serviceToken=test-token, got %q", c.serviceToken)
	}
	if c.history == nil {
		t.Error("expected history map to be initialized")
	}
}

func TestClearHistory(t *testing.T) {
	c := NewClient("http://localhost", "tok", false)
	c.history[123] = []conversationMsg{{Role: "user", Content: "hi"}}

	c.ClearHistory(123)

	if _, exists := c.history[123]; exists {
		t.Error("expected history for chatID 123 to be cleared")
	}
}

func TestFindAIAgent_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/plugins/search" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("capability") != "ai:chat" {
			t.Errorf("unexpected capability query: %s", r.URL.Query().Get("capability"))
		}
		resp := searchResponse{
			Plugins: []pluginInfo{
				{ID: "agent-openai", Status: "running"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-token", false)
	agentID, err := c.FindAIAgent(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agentID != "agent-openai" {
		t.Errorf("expected agent-openai, got %q", agentID)
	}
}

func TestFindAIAgent_NoRunning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := searchResponse{
			Plugins: []pluginInfo{
				{ID: "agent-openai", Status: "stopped"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-token", false)
	_, err := c.FindAIAgent(context.Background())
	if err == nil {
		t.Fatal("expected error when no running agent")
	}
}

func TestFindAIAgent_Caching(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		resp := searchResponse{
			Plugins: []pluginInfo{
				{ID: "agent-openai", Status: "running"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-token", false)

	// First call should hit the server.
	c.FindAIAgent(context.Background())
	// Second call should use cache.
	c.FindAIAgent(context.Background())

	if callCount != 1 {
		t.Errorf("expected 1 server call (cached), got %d", callCount)
	}
}

func TestTruncateStr(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
	}
	for _, tt := range tests {
		got := truncateStr(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncateStr(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

