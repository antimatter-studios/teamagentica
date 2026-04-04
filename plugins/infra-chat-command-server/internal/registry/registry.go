package registry

import (
	"sync"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

// Entry is a command spec plus the owning plugin ID.
type Entry struct {
	PluginID string                  `json:"plugin_id"`
	Command  pluginsdk.ChatCommand   `json:"command"`
}

// Registry holds the aggregated command index.
type Registry struct {
	mu      sync.RWMutex
	entries []Entry // all registered commands
	seq     uint64  // bumped on every mutation
}

// New creates an empty registry.
func New() *Registry {
	return &Registry{}
}

// Register adds (or replaces) all commands for a given plugin.
// Returns the new sequence number.
func (r *Registry) Register(pluginID string, commands []pluginsdk.ChatCommand) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Remove old entries from this plugin.
	filtered := r.entries[:0]
	for _, e := range r.entries {
		if e.PluginID != pluginID {
			filtered = append(filtered, e)
		}
	}

	// Add new entries.
	for _, cmd := range commands {
		filtered = append(filtered, Entry{PluginID: pluginID, Command: cmd})
	}
	r.entries = filtered
	r.seq++
	return r.seq
}

// Deregister removes all commands owned by a plugin.
// Returns the new sequence number and whether anything was removed.
func (r *Registry) Deregister(pluginID string) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	before := len(r.entries)
	filtered := r.entries[:0]
	for _, e := range r.entries {
		if e.PluginID != pluginID {
			filtered = append(filtered, e)
		}
	}
	r.entries = filtered

	if len(r.entries) < before {
		r.seq++
		return r.seq, true
	}
	return r.seq, false
}

// All returns a snapshot of all registered entries.
func (r *Registry) All() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Entry, len(r.entries))
	copy(out, r.entries)
	return out
}

// Lookup finds the entry for a given command name (optionally namespaced as "namespace:name").
func (r *Registry) Lookup(name string) (Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, e := range r.entries {
		// Match by full qualified name (namespace:name) or bare name.
		fqn := e.Command.Name
		if e.Command.Namespace != "" {
			fqn = e.Command.Namespace + ":" + e.Command.Name
		}
		if fqn == name || e.Command.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Seq returns the current sequence number.
func (r *Registry) Seq() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.seq
}
