package handlers

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

// discoveredTool holds a tool schema from a plugin with its routing info.
type discoveredTool struct {
	PluginID     string          `json:"plugin_id"`
	Name         string          `json:"name"`          // original name from plugin
	PrefixedName string          `json:"prefixed_name"` // pluginID__name
	Description  string          `json:"description"`
	Endpoint     string          `json:"endpoint"`
	Parameters   json.RawMessage `json:"parameters"`
}

// toolCache holds discovered tools with a TTL.
type toolCache struct {
	mu        sync.RWMutex
	tools     []discoveredTool
	fetchedAt time.Time
	ttl       time.Duration
}

var globalToolCache = &toolCache{ttl: 60 * time.Second}

// discoverTools queries the kernel for tool:* plugins and fetches their tool schemas.
func discoverTools(sdk *pluginsdk.Client) []discoveredTool {
	if sdk == nil {
		return nil
	}

	globalToolCache.mu.RLock()
	if time.Since(globalToolCache.fetchedAt) < globalToolCache.ttl && globalToolCache.tools != nil {
		tools := globalToolCache.tools
		globalToolCache.mu.RUnlock()
		return tools
	}
	globalToolCache.mu.RUnlock()

	globalToolCache.mu.Lock()
	defer globalToolCache.mu.Unlock()

	// Double-check after acquiring write lock.
	if time.Since(globalToolCache.fetchedAt) < globalToolCache.ttl && globalToolCache.tools != nil {
		return globalToolCache.tools
	}

	plugins, err := sdk.SearchPlugins("tool:")
	if err != nil {
		log.Printf("agent-claude: tool discovery failed: %v", err)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var allTools []discoveredTool
	for _, p := range plugins {
		body, err := sdk.RouteToPlugin(ctx, p.ID, "GET", "/mcp", nil)
		if err != nil {
			log.Printf("agent-claude: failed to get tools from %s: %v", p.ID, err)
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
			log.Printf("agent-claude: failed to parse tools from %s: %v", p.ID, err)
			continue
		}

		for _, t := range resp.Tools {
			allTools = append(allTools, discoveredTool{
				PluginID:     p.ID,
				Name:         t.Name,
				PrefixedName: p.ID + "__" + t.Name,
				Description:  t.Description,
				Endpoint:     t.Endpoint,
				Parameters:   t.Parameters,
			})
		}
	}

	globalToolCache.tools = allTools
	globalToolCache.fetchedAt = time.Now()

	if len(allTools) > 0 {
		names := make([]string, len(allTools))
		for i, t := range allTools {
			names[i] = t.PrefixedName
		}
		log.Printf("agent-claude: discovered %d tools: %s", len(allTools), strings.Join(names, ", "))
	}

	return allTools
}

