package router

import (
	"testing"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
)

func TestNewTable(t *testing.T) {
	table := NewTable()
	if table == nil {
		t.Fatal("NewTable returned nil")
	}
	if table.coordinators == nil {
		t.Error("coordinators map not initialized")
	}
	if table.workspaces == nil {
		t.Error("workspaces map not initialized")
	}
}

// --- Coordinator tests ---

func TestSetGetCoordinator(t *testing.T) {
	table := NewTable()

	table.SetCoordinator("messaging-discord", "agent-claude", "claude-sonnet-4-6")

	coord := table.GetCoordinator("messaging-discord")
	if coord == nil {
		t.Fatal("expected coordinator, got nil")
	}
	if coord.PluginID != "agent-claude" {
		t.Errorf("expected plugin_id=agent-claude, got %q", coord.PluginID)
	}
	if coord.Model != "claude-sonnet-4-6" {
		t.Errorf("expected model=claude-sonnet-4-6, got %q", coord.Model)
	}
}

func TestGetCoordinator_Missing(t *testing.T) {
	table := NewTable()
	if coord := table.GetCoordinator("nonexistent"); coord != nil {
		t.Errorf("expected nil for missing coordinator, got %+v", coord)
	}
}

func TestSetCoordinator_Overwrite(t *testing.T) {
	table := NewTable()

	table.SetCoordinator("messaging-discord", "agent-claude", "model-a")
	table.SetCoordinator("messaging-discord", "agent-openai", "model-b")

	coord := table.GetCoordinator("messaging-discord")
	if coord.PluginID != "agent-openai" {
		t.Errorf("expected overwritten coordinator=agent-openai, got %q", coord.PluginID)
	}
}

func TestSetCoordinator_MultipleSourcePlugins(t *testing.T) {
	table := NewTable()

	table.SetCoordinator("messaging-discord", "agent-claude", "")
	table.SetCoordinator("messaging-telegram", "agent-openai", "gpt-4o")

	discord := table.GetCoordinator("messaging-discord")
	telegram := table.GetCoordinator("messaging-telegram")

	if discord.PluginID != "agent-claude" {
		t.Errorf("discord coordinator: expected agent-claude, got %q", discord.PluginID)
	}
	if telegram.PluginID != "agent-openai" {
		t.Errorf("telegram coordinator: expected agent-openai, got %q", telegram.PluginID)
	}
}

func TestListCoordinators(t *testing.T) {
	table := NewTable()

	table.SetCoordinator("messaging-discord", "agent-claude", "")
	table.SetCoordinator("messaging-telegram", "agent-openai", "")

	list := table.ListCoordinators()
	if len(list) != 2 {
		t.Fatalf("expected 2 coordinators, got %d", len(list))
	}
	if list["messaging-discord"].PluginID != "agent-claude" {
		t.Error("missing or wrong discord coordinator")
	}
	if list["messaging-telegram"].PluginID != "agent-openai" {
		t.Error("missing or wrong telegram coordinator")
	}
}

func TestListCoordinators_ReturnsCopy(t *testing.T) {
	table := NewTable()
	table.SetCoordinator("messaging-discord", "agent-claude", "")

	list := table.ListCoordinators()
	delete(list, "messaging-discord")

	// Original should be unaffected.
	if coord := table.GetCoordinator("messaging-discord"); coord == nil {
		t.Error("deleting from ListCoordinators result should not affect table")
	}
}

// --- Workspace tests ---

func TestMapGetWorkspace(t *testing.T) {
	table := NewTable()

	table.MapWorkspace("messaging-discord", "chan-123", "ws-abc", "10.0.0.1:9999")

	ws := table.GetWorkspace("messaging-discord", "chan-123")
	if ws == nil {
		t.Fatal("expected workspace route, got nil")
	}
	if ws.WorkspaceID != "ws-abc" {
		t.Errorf("expected workspace_id=ws-abc, got %q", ws.WorkspaceID)
	}
	if ws.BridgeAddr != "10.0.0.1:9999" {
		t.Errorf("expected bridge_addr=10.0.0.1:9999, got %q", ws.BridgeAddr)
	}
}

func TestGetWorkspace_Missing(t *testing.T) {
	table := NewTable()
	if ws := table.GetWorkspace("messaging-discord", "nonexistent"); ws != nil {
		t.Errorf("expected nil for missing workspace, got %+v", ws)
	}
}

func TestGetWorkspace_WrongSourcePlugin(t *testing.T) {
	table := NewTable()
	table.MapWorkspace("messaging-discord", "chan-123", "ws-abc", "addr")

	// Same channel but different source should not match.
	if ws := table.GetWorkspace("messaging-telegram", "chan-123"); ws != nil {
		t.Errorf("expected nil for wrong source_plugin, got %+v", ws)
	}
}

func TestUnmapWorkspace(t *testing.T) {
	table := NewTable()

	table.MapWorkspace("messaging-discord", "chan-123", "ws-abc", "addr")
	table.UnmapWorkspace("messaging-discord", "chan-123")

	if ws := table.GetWorkspace("messaging-discord", "chan-123"); ws != nil {
		t.Errorf("expected nil after unmap, got %+v", ws)
	}
}

func TestUnmapWorkspace_Nonexistent(t *testing.T) {
	table := NewTable()
	// Should not panic.
	table.UnmapWorkspace("messaging-discord", "nonexistent")
}

func TestMapWorkspace_Overwrite(t *testing.T) {
	table := NewTable()

	table.MapWorkspace("messaging-discord", "chan-123", "ws-old", "addr-old")
	table.MapWorkspace("messaging-discord", "chan-123", "ws-new", "addr-new")

	ws := table.GetWorkspace("messaging-discord", "chan-123")
	if ws.WorkspaceID != "ws-new" {
		t.Errorf("expected overwritten workspace=ws-new, got %q", ws.WorkspaceID)
	}
}

func TestListWorkspaces(t *testing.T) {
	table := NewTable()

	table.MapWorkspace("messaging-discord", "chan-1", "ws-a", "addr-a")
	table.MapWorkspace("messaging-telegram", "chan-2", "ws-b", "addr-b")

	list := table.ListWorkspaces()
	if len(list) != 2 {
		t.Fatalf("expected 2 workspace mappings, got %d", len(list))
	}
	if ws, ok := list["messaging-discord/chan-1"]; !ok || ws.WorkspaceID != "ws-a" {
		t.Error("missing or wrong discord workspace mapping")
	}
	if ws, ok := list["messaging-telegram/chan-2"]; !ok || ws.WorkspaceID != "ws-b" {
		t.Error("missing or wrong telegram workspace mapping")
	}
}

// --- Alias tests ---

func TestAliases_InitiallyNil(t *testing.T) {
	table := NewTable()
	if table.Aliases() != nil {
		t.Error("expected nil aliases initially")
	}
}

func TestSetAliases(t *testing.T) {
	table := NewTable()

	aliases := alias.NewAliasMap([]alias.AliasInfo{
		{Name: "claude", Target: "agent-claude", Capabilities: []string{"agent"}},
		{Name: "codex", Target: "agent-openai", Capabilities: []string{"agent"}},
	})
	table.SetAliases(aliases)

	got := table.Aliases()
	if got == nil {
		t.Fatal("expected non-nil aliases")
	}
	if got.IsEmpty() {
		t.Error("expected non-empty aliases")
	}

	// Verify resolution works through the table.
	target := got.Resolve("claude")
	if target == nil {
		t.Fatal("expected to resolve 'claude'")
	}
	if target.PluginID != "agent-claude" {
		t.Errorf("expected plugin_id=agent-claude, got %q", target.PluginID)
	}
}

func TestSetAliases_Replace(t *testing.T) {
	table := NewTable()

	table.SetAliases(alias.NewAliasMap([]alias.AliasInfo{
		{Name: "claude", Target: "agent-claude", Capabilities: []string{"agent"}},
	}))

	// Replace with different set.
	table.SetAliases(alias.NewAliasMap([]alias.AliasInfo{
		{Name: "gemini", Target: "agent-gemini", Capabilities: []string{"agent"}},
	}))

	aliases := table.Aliases()
	if aliases.Resolve("claude") != nil {
		t.Error("expected old alias 'claude' to be gone")
	}
	if aliases.Resolve("gemini") == nil {
		t.Error("expected new alias 'gemini' to be present")
	}
}
