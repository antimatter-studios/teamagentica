package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
)

// discoveredTool holds a tool from a plugin with routing info.
type discoveredTool struct {
	PluginID   string          `json:"plugin_id"`
	AliasName  string          `json:"alias_name,omitempty"` // alias that generated this entry (e.g. "nb2")
	AliasModel string          `json:"alias_model,omitempty"` // model from alias (e.g. "gemini-3.1-flash-image-preview")
	Name       string          `json:"name"`
	FullName   string          `json:"full_name"` // aliasName__toolName or pluginID__toolName
	Desc       string          `json:"description"`
	Endpoint   string          `json:"endpoint"`
	Parameters json.RawMessage `json:"parameters"`
}

// inputSchema returns the Parameters as a JSON schema, defaulting to empty object.
func (dt discoveredTool) inputSchema() json.RawMessage {
	if dt.Parameters != nil && len(dt.Parameters) > 0 {
		return dt.Parameters
	}
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

// toolCache caches discovered tools with TTL.
type toolCache struct {
	mu        sync.RWMutex
	tools     []discoveredTool
	fetchedAt time.Time
	ttl       time.Duration
}

var cache = &toolCache{ttl: 60 * time.Second}

// pushedToolStore stores tools registered via POST /tools/register.
type pushedToolStore struct {
	mu    sync.RWMutex
	tools map[string][]rawTool // plugin_id → tools
}

// rawTool is a tool definition from a plugin.
type rawTool struct {
	PluginID    string
	Name        string
	Description string
	Endpoint    string
	Parameters  json.RawMessage
}

var pushed = &pushedToolStore{tools: make(map[string][]rawTool)}

// RegisterPushedTools stores tools for a plugin, replacing any previous entry.
func RegisterPushedTools(pluginID string, tools []rawTool) {
	pushed.mu.Lock()
	pushed.tools[pluginID] = tools
	pushed.mu.Unlock()
	log.Printf("mcp-server: %s pushed %d tools", pluginID, len(tools))
}

// pushedToolsByPlugin returns a copy of all pushed tools keyed by plugin ID.
func pushedToolsByPlugin() map[string][]rawTool {
	pushed.mu.RLock()
	defer pushed.mu.RUnlock()
	out := make(map[string][]rawTool, len(pushed.tools))
	for k, v := range pushed.tools {
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

// DiscoverTools queries kernel for tool:* plugins and builds MCP tool entries.
// If aliases are provided, tool-type aliases generate alias-named entries
// (e.g. "nb2__generate_image") so the coordinator can match @mentions to tools.
// Plugins without aliases still get raw plugin-named entries as fallback.
func DiscoverTools(sdk *pluginsdk.Client, aliases *alias.AliasMap) []discoveredTool {
	if sdk == nil {
		return nil
	}

	cache.mu.RLock()
	if time.Since(cache.fetchedAt) < cache.ttl && cache.tools != nil {
		tools := cache.tools
		cache.mu.RUnlock()
		return tools
	}
	cache.mu.RUnlock()

	cache.mu.Lock()
	defer cache.mu.Unlock()

	if time.Since(cache.fetchedAt) < cache.ttl && cache.tools != nil {
		return cache.tools
	}

	plugins, err := sdk.SearchPlugins("tool:")
	if err != nil {
		log.Printf("mcp-server: tool discovery failed: %v", err)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Start with pushed tools (registered via POST /tools/register).
	pluginTools := pushedToolsByPlugin()

	// Pull-based fallback: fetch from plugins that haven't pushed.
	for _, p := range plugins {
		if _, hasPushed := pluginTools[p.ID]; hasPushed {
			continue // already have pushed tools for this plugin
		}

		body, err := sdk.RouteToPlugin(ctx, p.ID, "GET", "/mcp", nil)
		if err != nil {
			log.Printf("mcp-server: failed to get tools from %s: %v", p.ID, err)
			continue
		}

		var resp struct {
			Tools []struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Endpoint    string          `json:"endpoint"`
				Parameters  json.RawMessage `json:"parameters"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			log.Printf("mcp-server: failed to parse tools from %s: %v", p.ID, err)
			continue
		}

		for _, t := range resp.Tools {
			pluginTools[p.ID] = append(pluginTools[p.ID], rawTool{
				PluginID:    p.ID,
				Name:        t.Name,
				Description: t.Description,
				Endpoint:    t.Endpoint,
				Parameters:  t.Parameters,
			})
		}
	}

	// Build alias-based tool entries.
	var allTools []discoveredTool
	coveredPlugins := make(map[string]bool) // plugins that have at least one alias

	if aliases != nil {
		for _, entry := range aliases.List() {
			pluginID := entry.Target.PluginID

			switch entry.Target.Type {
			case alias.TargetImage, alias.TargetVideo, alias.TargetStorage:
				// Tool alias — create alias-named entries for each plugin tool.
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

					allTools = append(allTools, discoveredTool{
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
				// Agent alias — create a chat tool so the coordinator can delegate.
				modelDesc := ""
				if entry.Target.Model != "" {
					modelDesc = fmt.Sprintf(" using model %s", entry.Target.Model)
				}
				desc := fmt.Sprintf("Send a message to @%s (%s%s) and get a response. Use this when the user wants to talk to or delegate a task to @%s.",
					entry.Alias, pluginID, modelDesc, entry.Alias)

				allTools = append(allTools, discoveredTool{
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

	// Add raw plugin-named tools for plugins without any alias coverage.
	for pluginID, tools := range pluginTools {
		if coveredPlugins[pluginID] {
			continue
		}
		for _, t := range tools {
			allTools = append(allTools, discoveredTool{
				PluginID:   t.PluginID,
				Name:       t.Name,
				FullName:   t.PluginID + "__" + t.Name,
				Desc:       t.Description,
				Endpoint:   t.Endpoint,
				Parameters: t.Parameters,
			})
		}
	}

	cache.tools = allTools
	cache.fetchedAt = time.Now()

	if len(allTools) > 0 {
		names := make([]string, len(allTools))
		for i, t := range allTools {
			names[i] = t.FullName
		}
		log.Printf("mcp-server: discovered %d tools: %s", len(allTools), strings.Join(names, ", "))
	}

	return allTools
}

// InvalidateCache forces re-discovery on next call.
func InvalidateCache() {
	cache.mu.Lock()
	cache.tools = nil
	cache.fetchedAt = time.Time{}
	cache.mu.Unlock()
}
