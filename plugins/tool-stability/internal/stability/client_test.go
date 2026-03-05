package stability

import "testing"

func TestDefaultModels(t *testing.T) {
	models := DefaultModels()
	if len(models) == 0 {
		t.Fatal("expected non-empty default models list")
	}

	expected := map[string]bool{
		"sd3-medium":      false,
		"sd3-large":       false,
		"sd3-large-turbo": false,
	}
	for _, m := range models {
		if _, ok := expected[m]; ok {
			expected[m] = true
		}
	}
	for model, found := range expected {
		if !found {
			t.Errorf("expected model %s in defaults", model)
		}
	}
}

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
	c := NewClient("key123", "sd3-medium", true)
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.apiKey != "key123" {
		t.Errorf("expected apiKey=key123, got %s", c.apiKey)
	}
	if c.model != "sd3-medium" {
		t.Errorf("expected model=sd3-medium, got %s", c.model)
	}
	if !c.debug {
		t.Error("expected debug=true")
	}
}
