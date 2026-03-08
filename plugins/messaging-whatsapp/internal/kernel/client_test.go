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

func TestChatWithAgentDirect_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/route/agent-openai/chat" {
			resp := chatResponse{Response: "Hello!"}
			json.NewEncoder(w).Encode(resp)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "tok", false)
	response, err := c.ChatWithAgentDirect("chat1", "agent-openai", "", "hi", nil, "")
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
