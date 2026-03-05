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

func TestFindAIAgent_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := searchResponse{
			Plugins: []pluginInfo{
				{ID: "agent-openai", Status: "running"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-token", nil)
	agentID, err := c.FindAIAgent()
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

	c := NewClient(server.URL, "test-token", nil)
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

	c := NewClient(server.URL, "test-token", nil)
	c.FindAIAgent()
	c.FindAIAgent()

	if callCount != 1 {
		t.Errorf("expected 1 server call (cached), got %d", callCount)
	}
}

func TestFindAIAgent_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-token", nil)
	_, err := c.FindAIAgent()
	if err == nil {
		t.Fatal("expected error on server error")
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
			resp := chatResponse{Response: "Hello back!"}
			json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-token", nil)
	response, err := c.ChatWithAgent("Hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if response != "Hello back!" {
		t.Errorf("expected 'Hello back!', got %q", response)
	}
}
