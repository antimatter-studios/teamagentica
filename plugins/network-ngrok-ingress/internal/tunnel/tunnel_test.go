package tunnel

import (
	"testing"
)

func TestNewManager(t *testing.T) {
	m := NewManager("auth-token", "my-domain", "localhost:8080")
	if m.authToken != "auth-token" {
		t.Errorf("expected authToken=auth-token, got %q", m.authToken)
	}
	if m.domain != "my-domain" {
		t.Errorf("expected domain=my-domain, got %q", m.domain)
	}
	if m.target != "localhost:8080" {
		t.Errorf("expected target=localhost:8080, got %q", m.target)
	}
}

func TestURL_InitiallyEmpty(t *testing.T) {
	m := NewManager("auth", "domain", "target")
	if url := m.URL(); url != "" {
		t.Errorf("expected empty URL initially, got %q", url)
	}
}

func TestClose_NilForwarder(t *testing.T) {
	m := NewManager("auth", "domain", "target")
	// Close should not error when forwarder is nil.
	if err := m.Close(); err != nil {
		t.Errorf("unexpected error closing nil forwarder: %v", err)
	}
}
