package seedance

import "testing"

func TestNewClient(t *testing.T) {
	c := NewClient("key123", true)
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.apiKey != "key123" {
		t.Errorf("expected apiKey=key123, got %s", c.apiKey)
	}
	if !c.debug {
		t.Error("expected debug=true")
	}
}

func TestNewClientDefaults(t *testing.T) {
	c := NewClient("", false)
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
