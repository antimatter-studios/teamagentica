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
	TargetAgent TargetType = iota
	TargetImage
	TargetVideo
)

// Target describes the plugin an alias resolves to.
type Target struct {
	PluginID string
	Model    string // optional, agents only
	Type     TargetType
}

// AliasEntry is a single alias→target pair for listing.
type AliasEntry struct {
	Alias  string
	Target Target
}

// AliasInfo is a structured alias descriptor received from the kernel.
// Capabilities are the plugin's registered capabilities (e.g. "tool:image", "ai:chat").
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
		aliases[name] = targetFromInfo(target, info.Capabilities)
	}
	return aliases
}

// targetFromInfo determines the Target from a plugin ID and its capabilities.
func targetFromInfo(pluginID string, capabilities []string) Target {
	// Check for model override: "agent-openai:gpt-4o".
	model := ""
	if idx := strings.Index(pluginID, ":"); idx > 0 {
		model = pluginID[idx+1:]
		pluginID = pluginID[:idx]
	}

	targetType := typeFromCapabilities(capabilities)
	return Target{PluginID: pluginID, Model: model, Type: targetType}
}

// typeFromCapabilities determines the target type from plugin capabilities.
func typeFromCapabilities(capabilities []string) TargetType {
	for _, cap := range capabilities {
		if strings.HasPrefix(cap, "tool:image") {
			return TargetImage
		}
		if strings.HasPrefix(cap, "tool:video") {
			return TargetVideo
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

// IsEmpty returns true if no aliases are configured.
func (m *AliasMap) IsEmpty() bool {
	aliases := m.getMap()
	return len(aliases) == 0
}

// ToolInfo describes a discovered tool for system prompt generation.
type ToolInfo struct {
	FullName    string // e.g. "nb2__generate_image" or "tool-nanobanana__generate_image"
	Description string
	PluginID    string
	AliasName   string // empty for anonymous tools
}

// SystemPromptBlock generates the alias-awareness section for the coordinator's
// system prompt. Returns "" if no aliases are configured.
func (m *AliasMap) SystemPromptBlock() string {
	return m.SystemPromptBlockWithTools(nil)
}

// SystemPromptBlockWithTools generates the coordinator system prompt with full
// tool/agent context. If discoveredTools is provided, anonymous (non-aliased)
// tools are also listed so the coordinator knows all available capabilities.
func (m *AliasMap) SystemPromptBlockWithTools(discoveredTools []ToolInfo) string {
	entries := m.List()
	if len(entries) == 0 && len(discoveredTools) == 0 {
		return ""
	}

	var agents, aliasedTools []AliasEntry
	for _, e := range entries {
		switch e.Target.Type {
		case TargetAgent:
			agents = append(agents, e)
		case TargetImage, TargetVideo:
			aliasedTools = append(aliasedTools, e)
		}
	}

	var sb strings.Builder
	sb.WriteString("You are the coordinator agent. You can answer directly or delegate to specialized agents and tools.\n\n")

	if len(agents) > 0 {
		sb.WriteString("AVAILABLE AGENTS:\n")
		for _, e := range agents {
			desc := e.Target.PluginID
			if e.Target.Model != "" {
				desc += " (model: " + e.Target.Model + ")"
			}
			sb.WriteString(fmt.Sprintf("- @%s → %s\n", e.Alias, desc))
		}
		sb.WriteString("\n")
	}

	if len(aliasedTools) > 0 {
		sb.WriteString("AVAILABLE TOOLS (aliased):\n")
		for _, e := range aliasedTools {
			toolType := "image generation"
			if e.Target.Type == TargetVideo {
				toolType = "video generation"
			}
			desc := fmt.Sprintf("%s via %s", toolType, e.Target.PluginID)
			if e.Target.Model != "" {
				desc += " (model: " + e.Target.Model + ")"
			}
			sb.WriteString(fmt.Sprintf("- @%s → %s\n", e.Alias, desc))
		}
		sb.WriteString("\n")
	}

	// List anonymous tools (not covered by any alias).
	aliasedPlugins := make(map[string]bool)
	for _, e := range entries {
		aliasedPlugins[e.Target.PluginID] = true
	}
	var anonTools []ToolInfo
	for _, t := range discoveredTools {
		if t.AliasName == "" && !aliasedPlugins[t.PluginID] {
			anonTools = append(anonTools, t)
		}
	}
	if len(anonTools) > 0 {
		sb.WriteString("AVAILABLE TOOLS (anonymous — no alias):\n")
		for _, t := range anonTools {
			desc := t.Description
			if desc == "" {
				desc = t.PluginID
			}
			sb.WriteString(fmt.Sprintf("- %s → %s\n", t.FullName, desc))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("ROUTING INSTRUCTIONS:\n")
	sb.WriteString("- If the user asks to delegate to a specific @agent or @tool, respond with EXACTLY:\nROUTE:@alias\nmessage to send\n")
	sb.WriteString("- For image/video requests, delegate to the appropriate tool alias.\n")
	sb.WriteString("- If you can answer directly, just respond normally.\n")
	sb.WriteString("- If the user's request involves multiple agents, handle what you can and suggest @mentions for the rest.\n")

	return sb.String()
}
