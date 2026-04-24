package stability

import "testing"

func TestTruncate(t *testing.T) {
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
		got := truncate(tc.input, tc.max)
		if got != tc.expected {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.input, tc.max, got, tc.expected)
		}
	}
}

func TestNewClient(t *testing.T) {
	c := NewClient("key123", "sd3.5-large", true)
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.apiKey != "key123" {
		t.Errorf("expected apiKey=key123, got %s", c.apiKey)
	}
	if c.model != "sd3.5-large" {
		t.Errorf("expected model=sd3.5-large, got %s", c.model)
	}
	if !c.debug {
		t.Error("expected debug=true")
	}
}
