package nanobanana

import "testing"

func TestTruncateBytes(t *testing.T) {
	tests := []struct {
		input    string
		max      int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
		{"exact", 5, "exact"},
	}

	for _, tc := range tests {
		got := truncateBytes([]byte(tc.input), tc.max)
		if got != tc.expected {
			t.Errorf("truncateBytes(%q, %d) = %q, want %q", tc.input, tc.max, got, tc.expected)
		}
	}
}

func TestNewClient(t *testing.T) {
	c := NewClient("key123", "gemini-2.5-flash-image", true)
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.apiKey != "key123" {
		t.Errorf("expected apiKey=key123, got %s", c.apiKey)
	}
	if c.defaultModel != "gemini-2.5-flash-image" {
		t.Errorf("expected defaultModel=gemini-2.5-flash-image, got %s", c.defaultModel)
	}
	if c.DefaultModel() != "gemini-2.5-flash-image" {
		t.Errorf("DefaultModel() mismatch")
	}
	if !c.debug {
		t.Error("expected debug=true")
	}
}

func TestNewClientDefaults(t *testing.T) {
	c := NewClient("", "test-model", false)
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.apiKey != "" {
		t.Errorf("expected empty apiKey, got %s", c.apiKey)
	}
	if c.debug {
		t.Error("expected debug=false")
	}
	if c.httpClient == nil {
		t.Error("expected non-nil httpClient")
	}
}
