package seedance

import "testing"

func TestNewClient(t *testing.T) {
	c := NewClient("key123", "seedance-2.0", true)
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.apiKey != "key123" {
		t.Errorf("expected apiKey=key123, got %s", c.apiKey)
	}
	if c.model != "seedance-2.0" {
		t.Errorf("expected model=seedance-2.0, got %s", c.model)
	}
	if !c.debug {
		t.Error("expected debug=true")
	}
}

func TestNewClientDefaults(t *testing.T) {
	c := NewClient("", "seedance-1.0-lite", false)
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
