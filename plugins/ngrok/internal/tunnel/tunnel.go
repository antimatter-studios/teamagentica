package tunnel

import (
	"context"
	"fmt"
	"net/url"
	"sync"

	"golang.ngrok.com/ngrok"
	"golang.ngrok.com/ngrok/config"
)

// Manager manages an ngrok tunnel that forwards traffic to a target address.
type Manager struct {
	authToken string
	domain    string
	target    string

	mu  sync.RWMutex
	fwd ngrok.Forwarder
	url string
}

// NewManager creates a new tunnel manager.
func NewManager(authToken, domain, target string) *Manager {
	return &Manager{
		authToken: authToken,
		domain:    domain,
		target:    target,
	}
}

// Start creates the ngrok tunnel and begins forwarding traffic to the target.
// Returns the public tunnel URL on success.
func (m *Manager) Start(ctx context.Context) (string, error) {
	targetURL, err := url.Parse("http://" + m.target)
	if err != nil {
		return "", fmt.Errorf("parse tunnel target: %w", err)
	}

	// Build endpoint config.
	opts := make([]config.HTTPEndpointOption, 0)
	if m.domain != "" {
		opts = append(opts, config.WithDomain(m.domain))
	}

	fwd, err := ngrok.ListenAndForward(ctx,
		targetURL,
		config.HTTPEndpoint(opts...),
		ngrok.WithAuthtoken(m.authToken),
	)
	if err != nil {
		return "", fmt.Errorf("ngrok listen and forward: %w", err)
	}

	m.mu.Lock()
	m.fwd = fwd
	m.url = fwd.URL()
	m.mu.Unlock()

	return fwd.URL(), nil
}

// URL returns the current public tunnel URL.
func (m *Manager) URL() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.url
}

// Close shuts down the tunnel.
func (m *Manager) Close() error {
	m.mu.RLock()
	fwd := m.fwd
	m.mu.RUnlock()

	if fwd != nil {
		return fwd.Close()
	}
	return nil
}
