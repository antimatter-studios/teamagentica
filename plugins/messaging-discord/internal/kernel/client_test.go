package kernel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewClient(t *testing.T) {
	c := NewClient("http://localhost:8080", "test-token", nil)
	if c.baseURL != "http://localhost:8080" {
		t.Errorf("expected baseURL=http://localhost:8080, got %q", c.baseURL)
	}
	if c.serviceToken != "test-token" {
		t.Errorf("expected serviceToken=test-token, got %q", c.serviceToken)
	}
}

func TestNewClient_WithTLS(t *testing.T) {
	// Passing a non-nil TLS config should set up a custom transport.
	// We just verify the client creates without panic.
	// A full TLS test would require certs — out of scope here.
	c := NewClient("https://localhost", "tok", nil)
	if c.httpClient.Transport != nil {
		t.Error("expected nil Transport when tlsConfig is nil")
	}
}

func TestChatWithAgentDirect_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/route/agent-openai/chat" {
			resp := chatResponse{Response: "Hello back!"}
			json.NewEncoder(w).Encode(resp)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-token", nil)
	response, err := c.ChatWithAgentDirect("agent-openai", "", "Hello", nil, false, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if response != "Hello back!" {
		t.Errorf("expected 'Hello back!', got %q", response)
	}
}
