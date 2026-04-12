package mcp

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
)

// registeredTool holds a tool from a plugin with routing info.
type registeredTool struct {
	PluginID   string          `json:"plugin_id"`
	AliasName  string          `json:"alias_name,omitempty"`
	AliasModel string          `json:"alias_model,omitempty"`
	Name       string          `json:"name"`
	FullName   string          `json:"full_name"` // pluginID__toolName
	Desc       string          `json:"description"`
	Endpoint   string          `json:"endpoint"`
	Parameters json.RawMessage `json:"parameters"`
}

// inputSchema returns the Parameters as a JSON schema, defaulting to empty object.
func (t registeredTool) inputSchema() json.RawMessage {
	if t.Parameters != nil && len(t.Parameters) > 0 {
		return t.Parameters
	}
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

// toolRegistry stores tools pushed by plugins and builds the final tool list.
type toolRegistry struct {
	mu    sync.RWMutex
	tools map[string][]rawTool // plugin_id → tools

	// cached built tool list
	cacheMu   sync.RWMutex
	cache     []registeredTool
	cacheTime time.Time
	cacheTTL  time.Duration
}

var registry = &toolRegistry{
	tools:    make(map[string][]rawTool),
	cacheTTL: 60 * time.Second,
}

// rawTool is a tool definition pushed by a plugin.
type rawTool struct {
	PluginID    string
	Name        string
	Description string
	Endpoint    string
	Parameters  json.RawMessage
}

// RegisterTools stores tools for a plugin, replacing any previous entry.
func RegisterTools(pluginID string, tools []rawTool) {
	registry.mu.Lock()
	registry.tools[pluginID] = tools
	registry.mu.Unlock()
	InvalidateToolCache()
	log.Printf("mcp-server: %s registered %d tools", pluginID, len(tools))
}

// toolsByPlugin returns a copy of all registered tools keyed by plugin ID.
func toolsByPlugin() map[string][]rawTool {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	out := make(map[string][]rawTool, len(registry.tools))
	for k, v := range registry.tools {
		out[k] = v
	}
	return out
}

// ToRawTools converts a handler-level tool list into rawTool entries.
func ToRawTools(pluginID string, tools []struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Endpoint    string          `json:"endpoint"`
	Parameters  json.RawMessage `json:"parameters"`
}) []rawTool {
	out := make([]rawTool, len(tools))
	for i, t := range tools {
		out[i] = rawTool{
			PluginID:    pluginID,
			Name:        t.Name,
			Description: t.Description,
			Endpoint:    t.Endpoint,
			Parameters:  t.Parameters,
		}
	}
	return out
}

// BuildToolList assembles the final tool list from registered tools + alias context.
func BuildToolList(aliases *alias.AliasMap) []registeredTool {
	registry.cacheMu.RLock()
	if time.Since(registry.cacheTime) < registry.cacheTTL && registry.cache != nil {
		tools := registry.cache
		registry.cacheMu.RUnlock()
		return tools
	}
	registry.cacheMu.RUnlock()

	registry.cacheMu.Lock()
	defer registry.cacheMu.Unlock()

	if time.Since(registry.cacheTime) < registry.cacheTTL && registry.cache != nil {
		return registry.cache
	}

	pluginTools := toolsByPlugin()

	var allTools []registeredTool
	coveredPlugins := make(map[string]bool)

	if aliases != nil {
		for _, entry := range aliases.List() {
			pluginID := entry.Target.PluginID

			switch entry.Target.Type {
			case alias.TargetImage, alias.TargetVideo, alias.TargetStorage:
				tools, ok := pluginTools[pluginID]
				if !ok {
					continue
				}

				coveredPlugins[pluginID] = true

				toolType := "image"
				if entry.Target.Type == alias.TargetVideo {
					toolType = "video"
				} else if entry.Target.Type == alias.TargetStorage {
					toolType = "storage"
				}

				for _, t := range tools {
					desc := t.Description
					if entry.Target.Model != "" {
						desc = fmt.Sprintf("%s (model: %s, alias: @%s)", desc, entry.Target.Model, entry.Alias)
					} else {
						desc = fmt.Sprintf("%s (alias: @%s, type: %s)", desc, entry.Alias, toolType)
					}

					allTools = append(allTools, registeredTool{
						PluginID:   pluginID,
						AliasName:  entry.Alias,
						AliasModel: entry.Target.Model,
						Name:       t.Name,
						FullName:   entry.Alias + "__" + t.Name,
						Desc:       desc,
						Endpoint:   t.Endpoint,
						Parameters: t.Parameters,
					})
				}

			case alias.TargetAgent:
				modelDesc := ""
				if entry.Target.Model != "" {
					modelDesc = fmt.Sprintf(" using model %s", entry.Target.Model)
				}
				desc := fmt.Sprintf("Send a message to @%s (%s%s) and get a response. Use this when the user wants to talk to or delegate a task to @%s.",
					entry.Alias, pluginID, modelDesc, entry.Alias)

				allTools = append(allTools, registeredTool{
					PluginID:   pluginID,
					AliasName:  entry.Alias,
					AliasModel: entry.Target.Model,
					Name:       "chat",
					FullName:   entry.Alias + "__chat",
					Desc:       desc,
					Endpoint:   "/chat",
					Parameters: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string","description":"The message to send to this agent"}},"required":["message"]}`),
				})
			}
		}
	}

	// Add raw plugin-named tools for plugins without alias coverage.
	for pluginID, tools := range pluginTools {
		if coveredPlugins[pluginID] {
			continue
		}
		for _, t := range tools {
			allTools = append(allTools, registeredTool{
				PluginID:   t.PluginID,
				Name:       t.Name,
				FullName:   t.PluginID + "__" + t.Name,
				Desc:       t.Description,
				Endpoint:   t.Endpoint,
				Parameters: t.Parameters,
			})
		}
	}

	registry.cache = allTools
	registry.cacheTime = time.Now()

	if len(allTools) > 0 {
		names := make([]string, len(allTools))
		for i, t := range allTools {
			names[i] = t.FullName
		}
		log.Printf("mcp-server: registered %d tools: %s", len(allTools), strings.Join(names, ", "))
	}

	return allTools
}

// InvalidateToolCache forces rebuild on next call.
func InvalidateToolCache() {
	registry.cacheMu.Lock()
	registry.cache = nil
	registry.cacheTime = time.Time{}
	registry.cacheMu.Unlock()
}
