package kernel

import (
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
	c.history["chat1"] = []conversationMsg{{Role: "user", Content: "hi"}}

	c.ClearHistory("chat1")

	if _, exists := c.history["chat1"]; exists {
		t.Error("expected history for chat1 to be cleared")
	}
}

func TestFindAIAgent_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := searchResponse{
			Plugins: []pluginInfo{
				{ID: "agent-gemini", Status: "running"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-token", false)
	agentID, err := c.FindAIAgent()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agentID != "agent-gemini" {
		t.Errorf("expected agent-gemini, got %q", agentID)
	}
}

func TestFindAIAgent_NoRunning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := searchResponse{
			Plugins: []pluginInfo{
				{ID: "agent-openai", Status: "stopped"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-token", false)
	_, err := c.FindAIAgent()
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
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewClient(server.URL, "tok", false)
	c.FindAIAgent()
	c.FindAIAgent()

	if callCount != 1 {
		t.Errorf("expected 1 server call (cached), got %d", callCount)
	}
}

func TestChatWithAgent_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/plugins/search":
			resp := searchResponse{
				Plugins: []pluginInfo{
					{ID: "agent-openai", Status: "running"},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case "/api/route/agent-openai/chat":
			resp := chatResponse{Response: "Hello!"}
			json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "tok", false)
	response, err := c.ChatWithAgent("chat1", "hi", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if response != "Hello!" {
		t.Errorf("expected 'Hello!', got %q", response)
	}

	// Verify history was recorded.
	c.histMu.Lock()
	hist := c.history["chat1"]
	c.histMu.Unlock()
	if len(hist) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(hist))
	}
	if hist[0].Role != "user" || hist[1].Role != "assistant" {
		t.Errorf("unexpected history roles: %v", hist)
	}
}
