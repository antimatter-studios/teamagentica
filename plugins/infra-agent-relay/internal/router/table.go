package router

import (
	"sync"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
)

// CoordinatorRoute maps a source plugin to its default coordinator agent.
type CoordinatorRoute struct {
	PluginID string `json:"plugin_id"`
	Model    string `json:"model,omitempty"`
}

// WorkspaceRoute maps a channel to a workspace bridge.
type WorkspaceRoute struct {
	WorkspaceID string `json:"workspace_id"`
	BridgeAddr  string `json:"bridge_addr"`
}

// routeKey is {source_plugin, channel_id}.
type routeKey struct {
	SourcePlugin string
	ChannelID    string
}

// Table is the relay's routing table. Thread-safe.
type Table struct {
	mu           sync.RWMutex
	coordinators map[string]*CoordinatorRoute  // source_plugin → coordinator
	workspaces   map[routeKey]*WorkspaceRoute  // {source, channel} → workspace
	aliases      *alias.AliasMap
}

// NewTable creates an empty routing table.
func NewTable() *Table {
	return &Table{
		coordinators: make(map[string]*CoordinatorRoute),
		workspaces:   make(map[routeKey]*WorkspaceRoute),
	}
}

// SetCoordinator sets the coordinator agent for a source plugin.
func (t *Table) SetCoordinator(sourcePlugin, pluginID, model string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.coordinators[sourcePlugin] = &CoordinatorRoute{PluginID: pluginID, Model: model}
}

// GetCoordinator returns the coordinator for a source plugin, or nil.
func (t *Table) GetCoordinator(sourcePlugin string) *CoordinatorRoute {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.coordinators[sourcePlugin]
}

// MapWorkspace maps a {source, channel} to a workspace bridge.
func (t *Table) MapWorkspace(sourcePlugin, channelID, workspaceID, bridgeAddr string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.workspaces[routeKey{sourcePlugin, channelID}] = &WorkspaceRoute{
		WorkspaceID: workspaceID,
		BridgeAddr:  bridgeAddr,
	}
}

// UnmapWorkspace removes a channel→workspace mapping.
func (t *Table) UnmapWorkspace(sourcePlugin, channelID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.workspaces, routeKey{sourcePlugin, channelID})
}

// GetWorkspace returns the workspace route for a channel, or nil.
func (t *Table) GetWorkspace(sourcePlugin, channelID string) *WorkspaceRoute {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.workspaces[routeKey{sourcePlugin, channelID}]
}

// SetAliases replaces the alias map.
func (t *Table) SetAliases(aliases *alias.AliasMap) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.aliases = aliases
}

// Aliases returns the current alias map (may be nil).
func (t *Table) Aliases() *alias.AliasMap {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.aliases
}

// ListCoordinators returns a snapshot of coordinator mappings.
func (t *Table) ListCoordinators() map[string]*CoordinatorRoute {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]*CoordinatorRoute, len(t.coordinators))
	for k, v := range t.coordinators {
		out[k] = v
	}
	return out
}

// ListWorkspaces returns a snapshot of workspace mappings.
func (t *Table) ListWorkspaces() map[string]*WorkspaceRoute {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]*WorkspaceRoute, len(t.workspaces))
	for k, v := range t.workspaces {
		out[k.SourcePlugin+"/"+k.ChannelID] = v
	}
	return out
}
