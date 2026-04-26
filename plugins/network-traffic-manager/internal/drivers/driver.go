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
//
// PublicKey is the SSH public key (authorized_keys format) the driver uses to
// authenticate to its remote endpoint, when applicable. The user must install
// it on the remote host before the tunnel can connect.
//
// Endpoint is the host:port a user can connect to from outside to use the
// tunnel (e.g. "s1.antimatter-studios.com:10022" for ssh-reverse).
type Status struct {
	State     string `json:"state"`
	URL       string `json:"url,omitempty"`
	Error     string `json:"error,omitempty"`
	PublicKey string `json:"public_key,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"`
	// LocalSocketPath is the local filesystem path (typically a unix socket) that consumers on the same host can connect to. Empty for drivers without a local terminus.
	LocalSocketPath string `json:"local_socket_path,omitempty"`
}

// Driver is a single tunnel implementation (ngrok, ssh, ...). Each driver
// instance is bound to exactly one named tunnel.
type Driver interface {
	Start(ctx context.Context) (Status, error)
	Stop(ctx context.Context) error
	Status() Status
}

// Factory builds a Driver from a tunnel name + target address and a
// driver-specific config map. The name is used by drivers that need stable,
// per-tunnel on-disk state (e.g. auto-generated SSH keys).
type Factory func(name, target string, cfg map[string]string) (Driver, error)

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

// Build constructs a driver instance for id, identified by name.
func Build(id, name, target string, cfg map[string]string) (Driver, error) {
	regMu.RLock()
	f := registered[id]
	regMu.RUnlock()
	if f == nil {
		return nil, fmt.Errorf("unknown driver %q", id)
	}
	return f(name, target, cfg)
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
