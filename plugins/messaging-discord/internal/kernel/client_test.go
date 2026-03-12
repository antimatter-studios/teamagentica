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

func TestFindImageTool_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/plugins/search" {
			resp := searchResponse{Plugins: []pluginInfo{{ID: "tool-stability", Status: "running"}}}
			json.NewEncoder(w).Encode(resp)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-token", nil)
	pluginID, err := c.FindImageTool("stability")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pluginID != "tool-stability" {
		t.Errorf("expected 'tool-stability', got %q", pluginID)
	}
}

func TestFindImageTool_NoRunningPlugin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/plugins/search" {
			resp := searchResponse{Plugins: []pluginInfo{
				{ID: "tool-stability", Status: "stopped"},
				{ID: "tool-dalle", Status: "installing"},
			}}
			json.NewEncoder(w).Encode(resp)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-token", nil)
	_, err := c.FindImageTool("stability")
	if err == nil {
		t.Fatal("expected error when no running plugin, got nil")
	}
}

func TestFindVideoTool_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/plugins/search" {
			q := r.URL.Query().Get("capability")
			if q != "tool:video:veo" {
				t.Errorf("expected capability=tool:video:veo, got %q", q)
			}
			resp := searchResponse{Plugins: []pluginInfo{{ID: "tool-veo", Status: "running"}}}
			json.NewEncoder(w).Encode(resp)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-token", nil)
	pluginID, err := c.FindVideoTool("veo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pluginID != "tool-veo" {
		t.Errorf("expected 'tool-veo', got %q", pluginID)
	}
}

func TestGenerateImage_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/plugins/search":
			resp := searchResponse{Plugins: []pluginInfo{{ID: "tool-stability", Status: "running"}}}
			json.NewEncoder(w).Encode(resp)
		case "/api/route/tool-stability/generate":
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			resp := imageGenerateResponse{
				Status:    "success",
				ImageData: "dGVzdA==",
				MimeType:  "image/png",
				Seed:      "42",
				Model:     "sdxl",
				Prompt:    "a cat",
			}
			json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-token", nil)
	resp, err := c.GenerateImage("stability", "a cat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "success" {
		t.Errorf("expected status=success, got %q", resp.Status)
	}
	if resp.ImageData != "dGVzdA==" {
		t.Errorf("expected ImageData=dGVzdA==, got %q", resp.ImageData)
	}
	if resp.MimeType != "image/png" {
		t.Errorf("expected MimeType=image/png, got %q", resp.MimeType)
	}
	if resp.Seed != "42" {
		t.Errorf("expected Seed=42, got %q", resp.Seed)
	}
	if resp.Model != "sdxl" {
		t.Errorf("expected Model=sdxl, got %q", resp.Model)
	}
	if resp.Prompt != "a cat" {
		t.Errorf("expected Prompt='a cat', got %q", resp.Prompt)
	}
}

func TestGenerateImage_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/plugins/search":
			resp := searchResponse{Plugins: []pluginInfo{{ID: "tool-stability", Status: "running"}}}
			json.NewEncoder(w).Encode(resp)
		case "/api/route/tool-stability/generate":
			resp := imageGenerateResponse{
				Status: "error",
				Error:  "content policy violation",
			}
			json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-token", nil)
	_, err := c.GenerateImage("stability", "bad prompt")
	if err == nil {
		t.Fatal("expected error from generate, got nil")
	}
}

func TestGenerateVideo_Accepted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/plugins/search":
			resp := searchResponse{Plugins: []pluginInfo{{ID: "tool-veo", Status: "running"}}}
			json.NewEncoder(w).Encode(resp)
		case "/api/route/tool-veo/generate":
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			w.WriteHeader(http.StatusAccepted)
			resp := videoGenerateResponse{
				TaskID:  "task-123",
				Status:  "accepted",
				Message: "video generation started",
			}
			json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-token", nil)
	resp, err := c.GenerateVideo("veo", "a sunset timelapse")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TaskID != "task-123" {
		t.Errorf("expected TaskID=task-123, got %q", resp.TaskID)
	}
	if resp.Status != "accepted" {
		t.Errorf("expected Status=accepted, got %q", resp.Status)
	}
	if resp.Message != "video generation started" {
		t.Errorf("expected Message='video generation started', got %q", resp.Message)
	}
}

func TestCheckVideoStatus_Completed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/plugins/search":
			resp := searchResponse{Plugins: []pluginInfo{{ID: "tool-veo", Status: "running"}}}
			json.NewEncoder(w).Encode(resp)
		case "/api/route/tool-veo/status/task-123":
			if r.Method != http.MethodGet {
				t.Errorf("expected GET, got %s", r.Method)
			}
			resp := VideoStatusResponse{
				TaskID:   "task-123",
				Status:   "completed",
				VideoURI: "gs://bucket/video.mp4",
				VideoURL: "https://storage.example.com/video.mp4",
			}
			json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-token", nil)
	resp, err := c.CheckVideoStatus("veo", "task-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TaskID != "task-123" {
		t.Errorf("expected TaskID=task-123, got %q", resp.TaskID)
	}
	if resp.Status != "completed" {
		t.Errorf("expected Status=completed, got %q", resp.Status)
	}
	if resp.VideoURI != "gs://bucket/video.mp4" {
		t.Errorf("expected VideoURI=gs://bucket/video.mp4, got %q", resp.VideoURI)
	}
	if resp.VideoURL != "https://storage.example.com/video.mp4" {
		t.Errorf("expected VideoURL=https://storage.example.com/video.mp4, got %q", resp.VideoURL)
	}
}
