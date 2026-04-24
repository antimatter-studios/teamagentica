package drivers

import (
	"context"
	"fmt"
	"sync"
)

// State values.
const (
	StateStopped  = "stopped"
	StateStarting = "starting"
	StateRunning  = "running"
	StateError    = "error"
)

// Status is the runtime status of a driver instance.
type Status struct {
	State string `json:"state"`
	URL   string `json:"url,omitempty"`
	Error string `json:"error,omitempty"`
}

// Driver is a single tunnel implementation (ngrok, ssh, ...). Each driver
// instance is bound to exactly one named tunnel.
type Driver interface {
	Start(ctx context.Context) (Status, error)
	Stop(ctx context.Context) error
	Status() Status
}

// Factory builds a Driver from a target address and a driver-specific config
// map. Config keys are driver-defined.
type Factory func(target string, cfg map[string]string) (Driver, error)

var (
	regMu      sync.RWMutex
	registered = map[string]Factory{}
)

// Register makes a factory available under the given driver id. Called from
// driver package init() funcs.
func Register(id string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	registered[id] = f
}

// Build constructs a driver instance for id.
func Build(id, target string, cfg map[string]string) (Driver, error) {
	regMu.RLock()
	f := registered[id]
	regMu.RUnlock()
	if f == nil {
		return nil, fmt.Errorf("unknown driver %q", id)
	}
	return f(target, cfg)
}

// Available returns the list of registered driver ids.
func Available() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(registered))
	for k := range registered {
		out = append(out, k)
	}
	return out
}
