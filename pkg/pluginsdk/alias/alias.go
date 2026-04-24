package alias

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
)

// TargetType identifies the kind of plugin an alias points to.
type TargetType int

const (
	TargetAgent   TargetType = iota // agent:chat — has /chat, can be called directly
	TargetImage                     // agent:tool:image — has /chat, direct-callable
	TargetVideo                     // agent:tool:video — has /chat, direct-callable
	TargetStorage                   // storage:* — MCP-only, must go through an agent
	TargetTool                      // tool:* — MCP-only, must go through an agent
)

// Target describes the plugin an alias resolves to.
type Target struct {
	PluginID     string
	Model        string   // optional, agents only
	Type         TargetType
	Capabilities []string // original capabilities, used for prompt generation
}

// IsChatTarget returns true if this target has a /chat endpoint and can be
// addressed directly (agents and agent-tools like image/video generators).
func (t Target) IsChatTarget() bool {
	return t.Type == TargetAgent || t.Type == TargetImage || t.Type == TargetVideo
}

// AliasEntry is a single alias→target pair for listing.
type AliasEntry struct {
	Alias  string
	Target Target
}

// AliasInfo is a structured alias descriptor received from the kernel.
// Capabilities are the plugin's registered capabilities (e.g. "tool:image", "agent:chat").
type AliasInfo struct {
	Name         string   `json:"name"`
	Target       string   `json:"target"`
	Capabilities []string `json:"capabilities"`
}

// ParseResult is the outcome of parsing a message for an @mention.
type ParseResult struct {
	Alias     string
	Target    *Target // nil if no match
	Remainder string
}

// AliasMap holds configured alias→target mappings.
// Thread-safe: reads use atomic load, writes use atomic store via Replace().
type AliasMap struct {
	p atomic.Pointer[map[string]Target]
}

// getMap returns the current alias map via atomic load.
func (m *AliasMap) getMap() map[string]Target {
	if m == nil {
		return nil
	}
	ptr := m.p.Load()
	if ptr == nil {
		return nil
	}
	return *ptr
}

// NewAliasMap creates an alias map from structured alias info.
func NewAliasMap(infos []AliasInfo) *AliasMap {
	m := &AliasMap{}
	aliases := buildMap(infos)
	m.p.Store(&aliases)
	return m
}

// Replace atomically swaps the alias map with a new set of alias info.
// Safe to call from any goroutine while reads are in progress.
func (m *AliasMap) Replace(infos []AliasInfo) {
	aliases := buildMap(infos)
	m.p.Store(&aliases)
}

// buildMap converts structured alias infos into the internal map.
func buildMap(infos []AliasInfo) map[string]Target {
	aliases := make(map[string]Target, len(infos))
	for _, info := range infos {
		name := strings.ToLower(strings.TrimSpace(info.Name))
		target := strings.TrimSpace(info.Target)
		if name == "" || target == "" {
			continue
		}
		aliases[name] = TargetFromInfo(target, info.Capabilities)
	}
	return aliases
}

// TargetFromInfo determines the Target from a plugin ID and its capabilities.
func TargetFromInfo(pluginID string, capabilities []string) Target {
	// Check for model override: "agent-openai:gpt-4o".
	model := ""
	if idx := strings.Index(pluginID, ":"); idx > 0 {
		model = pluginID[idx+1:]
		pluginID = pluginID[:idx]
	}

	targetType := typeFromCapabilities(capabilities)
	return Target{PluginID: pluginID, Model: model, Type: targetType, Capabilities: capabilities}
}

// typeFromCapabilities determines the target type from plugin capabilities.
// agent:tool:* plugins have /chat and can be called directly (TargetImage/Video).
// tool:* and storage:* plugins are MCP-only (TargetTool/TargetStorage) and must
// be used through an agent.
func typeFromCapabilities(capabilities []string) TargetType {
	for _, cap := range capabilities {
		// agent:tool:* — direct-callable, has /chat endpoint.
		if strings.HasPrefix(cap, "agent:tool:image") {
			return TargetImage
		}
		if strings.HasPrefix(cap, "agent:tool:video") {
			return TargetVideo
		}
		// tool:* and storage:* — MCP-only, no /chat.
		if strings.HasPrefix(cap, "tool:storage") || strings.HasPrefix(cap, "storage:") {
			return TargetStorage
		}
		if strings.HasPrefix(cap, "tool:") {
			return TargetTool
		}
	}
	return TargetAgent
}



// Parse checks whether text starts with @alias and returns the match.
// Only matches at the start of the message (case-insensitive).
func (m *AliasMap) Parse(text string) ParseResult {
	aliases := m.getMap()
	if len(aliases) == 0 {
		return ParseResult{Remainder: text}
	}

	if !strings.HasPrefix(text, "@") {
		return ParseResult{Remainder: text}
	}

	rest := text[1:]
	spaceIdx := strings.IndexAny(rest, " \t\n")
	var aliasName, remainder string
	if spaceIdx < 0 {
		aliasName = rest
		remainder = ""
	} else {
		aliasName = rest[:spaceIdx]
		remainder = strings.TrimSpace(rest[spaceIdx+1:])
	}

	aliasLower := strings.ToLower(aliasName)
	if target, ok := aliases[aliasLower]; ok {
		return ParseResult{
			Alias:     aliasLower,
			Target:    &target,
			Remainder: remainder,
		}
	}

	return ParseResult{Remainder: text}
}

// Resolve looks up an alias by name (with or without @ prefix).
func (m *AliasMap) Resolve(alias string) *Target {
	aliases := m.getMap()
	if aliases == nil {
		return nil
	}
	alias = strings.ToLower(strings.TrimPrefix(alias, "@"))
	if t, ok := aliases[alias]; ok {
		return &t
	}
	return nil
}

// List returns all aliases sorted alphabetically.
func (m *AliasMap) List() []AliasEntry {
	aliases := m.getMap()
	if aliases == nil {
		return nil
	}
	entries := make([]AliasEntry, 0, len(aliases))
	for name, target := range aliases {
		entries = append(entries, AliasEntry{Alias: name, Target: target})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Alias < entries[j].Alias
	})
	return entries
}

// FindAliasByPluginID returns the alias name for a given plugin ID, or empty string if not found.
func (m *AliasMap) FindAliasByPluginID(pluginID string) string {
	aliases := m.getMap()
	if aliases == nil {
		return ""
	}
	for name, target := range aliases {
		if target.PluginID == pluginID {
			return name
		}
	}
	return ""
}

// ListAgentAliases returns sorted alias names that point to agent plugins.
func (m *AliasMap) ListAgentAliases() []string {
	aliases := m.getMap()
	if aliases == nil {
		return nil
	}
	var names []string
	for name, target := range aliases {
		if target.Type == TargetAgent {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// ListChattableAliases returns sorted alias names that have /chat endpoints
// (agents, image generators, video generators — but not plain tools or storage).
func (m *AliasMap) ListChattableAliases() []string {
	aliases := m.getMap()
	if aliases == nil {
		return nil
	}
	var names []string
	for name, target := range aliases {
		if target.IsChatTarget() {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// With returns a new AliasMap with the given alias added or overwritten.
// The receiver is not modified.
func (m *AliasMap) With(name string, target Target) *AliasMap {
	old := m.getMap()
	clone := make(map[string]Target, len(old)+1)
	for k, v := range old {
		clone[k] = v
	}
	clone[strings.ToLower(strings.TrimSpace(name))] = target
	result := &AliasMap{}
	result.p.Store(&clone)
	return result
}

// Set adds or replaces a single alias entry (copy-on-write).
func (m *AliasMap) Set(name string, target Target) {
	old := m.getMap()
	clone := make(map[string]Target, len(old)+1)
	for k, v := range old {
		clone[k] = v
	}
	clone[strings.ToLower(strings.TrimSpace(name))] = target
	m.p.Store(&clone)
}

// Remove deletes a single alias entry (copy-on-write). No-op if not found.
func (m *AliasMap) Remove(name string) {
	old := m.getMap()
	key := strings.ToLower(strings.TrimSpace(name))
	if _, ok := old[key]; !ok {
		return
	}
	clone := make(map[string]Target, len(old))
	for k, v := range old {
		if k != key {
			clone[k] = v
		}
	}
	m.p.Store(&clone)
}

// IsEmpty returns true if no aliases are configured.
func (m *AliasMap) IsEmpty() bool {
	aliases := m.getMap()
	return len(aliases) == 0
}

// ToolInfo describes a discovered tool for system prompt generation.
type ToolInfo struct {
	FullName    string // e.g. "nb2__generate_image" or "agent-nanobanana__generate_image"
	Name        string // short name, e.g. "generate_video"
	Description string
	PluginID    string
	AliasName   string                 // empty for anonymous tools
	Endpoint    string                 // e.g. "/generate", "/status/:taskId"
	Parameters  map[string]interface{} // JSON Schema for the tool's parameters
}

// SystemPromptBlock generates a simple alias-awareness section listing
// available agents and tools. Used by the MCP server to give agents
// awareness of the platform. The full coordinator prompt (with routing
// instructions) is now generated by the relay's template.
func (m *AliasMap) SystemPromptBlock() string {
	entries := m.List()
	if len(entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("Available agents and tools on this platform:\n")
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("- @%s → %s\n", e.Alias, e.Target.PluginID))
	}
	return sb.String()
}
