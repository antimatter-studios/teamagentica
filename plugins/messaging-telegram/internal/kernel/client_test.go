package kernel

import (
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

