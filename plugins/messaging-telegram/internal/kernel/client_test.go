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
	c.history[123] = true

	c.ClearHistory(123)

	if _, exists := c.history[123]; exists {
		t.Error("expected history for chatID 123 to be cleared")
	}
}

func TestFindImageTool_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/plugins/search" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		resp := searchResponse{
			Plugins: []pluginInfo{
				{ID: "img-stopped", Status: "stopped"},
				{ID: "img-running", Status: "running"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", false)
	id, err := c.FindImageTool(context.Background(), "stability")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "img-running" {
		t.Errorf("expected id=img-running, got %q", id)
	}
}

func TestFindImageTool_NoneRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := searchResponse{
			Plugins: []pluginInfo{
				{ID: "img-stopped", Status: "stopped"},
				{ID: "img-error", Status: "error"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", false)
	_, err := c.FindImageTool(context.Background(), "")
	if err == nil {
		t.Fatal("expected error when no running plugin, got nil")
	}
}

func TestFindVideoTool_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/plugins/search" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("capability"); got != "tool:video:veo" {
			t.Errorf("expected capability=tool:video:veo, got %q", got)
		}
		resp := searchResponse{
			Plugins: []pluginInfo{
				{ID: "vid-veo", Status: "running"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", false)
	id, err := c.FindVideoTool(context.Background(), "veo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "vid-veo" {
		t.Errorf("expected id=vid-veo, got %q", id)
	}
}

func TestGenerateImage_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/plugins/search":
			resp := searchResponse{
				Plugins: []pluginInfo{{ID: "img-1", Status: "running"}},
			}
			json.NewEncoder(w).Encode(resp)
		case "/api/route/img-1/generate":
			resp := imageGenerateResponse{
				Status:    "success",
				ImageData: "base64data",
				MimeType:  "image/png",
				Prompt:    "a cat",
			}
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", 404)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", false)
	resp, err := c.GenerateImage(context.Background(), "", "a cat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ImageData != "base64data" {
		t.Errorf("expected ImageData=base64data, got %q", resp.ImageData)
	}
	if resp.MimeType != "image/png" {
		t.Errorf("expected MimeType=image/png, got %q", resp.MimeType)
	}
}

func TestGenerateImage_ToolError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/plugins/search":
			resp := searchResponse{
				Plugins: []pluginInfo{{ID: "img-1", Status: "running"}},
			}
			json.NewEncoder(w).Encode(resp)
		case "/api/route/img-1/generate":
			resp := imageGenerateResponse{
				Error: "content policy violation",
			}
			json.NewEncoder(w).Encode(resp)
		default:
			http.Error(w, "not found", 404)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", false)
	_, err := c.GenerateImage(context.Background(), "", "bad prompt")
	if err == nil {
		t.Fatal("expected error when Error field is set, got nil")
	}
}

func TestGenerateVideo_Accepted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/plugins/search":
			resp := searchResponse{
				Plugins: []pluginInfo{{ID: "vid-1", Status: "running"}},
			}
			json.NewEncoder(w).Encode(resp)
		case "/api/route/vid-1/generate":
			w.WriteHeader(http.StatusAccepted)
			resp := videoGenerateResponse{
				TaskID:  "task-abc",
				Status:  "accepted",
				Message: "video generation started",
			}
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", 404)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", false)
	resp, err := c.GenerateVideo(context.Background(), "", "a sunset")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TaskID != "task-abc" {
		t.Errorf("expected TaskID=task-abc, got %q", resp.TaskID)
	}
	if resp.Status != "accepted" {
		t.Errorf("expected Status=accepted, got %q", resp.Status)
	}
}

func TestCheckVideoStatus_Completed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/plugins/search":
			resp := searchResponse{
				Plugins: []pluginInfo{{ID: "vid-1", Status: "running"}},
			}
			json.NewEncoder(w).Encode(resp)
		case "/api/route/vid-1/status/task-xyz":
			resp := videoStatusResponse{
				TaskID:   "task-xyz",
				Status:   "completed",
				VideoURL: "https://cdn.example.com/video.mp4",
			}
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", 404)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", false)
	resp, err := c.CheckVideoStatus(context.Background(), "", "task-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "completed" {
		t.Errorf("expected Status=completed, got %q", resp.Status)
	}
	if resp.VideoURL != "https://cdn.example.com/video.mp4" {
		t.Errorf("expected VideoURL=https://cdn.example.com/video.mp4, got %q", resp.VideoURL)
	}
}

func TestReportEvent(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/api/plugins/event" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", false)
	// Should not panic or error — fire-and-forget
	c.ReportEvent(context.Background(), "plugin-1", "info", "test detail")

	if !called {
		t.Error("expected event endpoint to be called")
	}
}
