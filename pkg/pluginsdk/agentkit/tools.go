package agentkit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

// DiscoveredTool holds a tool schema from a plugin with its routing info.
type DiscoveredTool struct {
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
	tools     []DiscoveredTool
	fetchedAt time.Time
	ttl       time.Duration
}

var globalToolCache = &toolCache{ttl: 60 * time.Second}

// DiscoverTools queries the platform for tool:* plugins and fetches their schemas.
// Results are cached with a 60s TTL to avoid redundant network calls.
func DiscoverTools(sdk *pluginsdk.Client) []DiscoveredTool {
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
		log.Printf("agentkit: tool discovery failed: %v", err)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var allTools []DiscoveredTool
	for _, p := range plugins {
		body, err := sdk.RouteToPlugin(ctx, p.ID, "GET", "/mcp", nil)
		if err != nil {
			log.Printf("agentkit: failed to get tools from %s: %v", p.ID, err)
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
			log.Printf("agentkit: failed to parse tools from %s: %v", p.ID, err)
			continue
		}

		for _, t := range resp.Tools {
			allTools = append(allTools, DiscoveredTool{
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
		log.Printf("agentkit: discovered %d tools: %s", len(allTools), strings.Join(names, ", "))
	}

	return allTools
}

// ToToolDefinitions converts discovered tools to the provider-agnostic format.
func ToToolDefinitions(tools []DiscoveredTool) []ToolDefinition {
	defs := make([]ToolDefinition, len(tools))
	for i, t := range tools {
		defs[i] = ToolDefinition{
			Name:        t.PrefixedName,
			Description: t.Description,
			Parameters:  t.Parameters,
		}
	}
	return defs
}

// ExecuteToolCall runs a single tool call by routing to the owning plugin via P2P.
// The tool name must be in prefixed format: "pluginID__toolName".
func ExecuteToolCall(sdk *pluginsdk.Client, tools []DiscoveredTool, call ToolCall) (string, error) {
	parts := strings.SplitN(call.Name, "__", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid tool name format: %s (expected pluginID__toolName)", call.Name)
	}
	pluginID, toolName := parts[0], parts[1]

	// Find the tool's endpoint from the cached tools.
	var endpoint string
	for _, t := range tools {
		if t.PluginID == pluginID && t.Name == toolName {
			endpoint = t.Endpoint
			break
		}
	}
	if endpoint == "" {
		return "", fmt.Errorf("tool %s not found on plugin %s", toolName, pluginID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var body *bytes.Reader
	if len(call.Arguments) > 0 {
		body = bytes.NewReader(call.Arguments)
	} else {
		body = bytes.NewReader([]byte("{}"))
	}

	resp, err := sdk.RouteToPlugin(ctx, pluginID, "POST", endpoint, body)
	if err != nil {
		return "", fmt.Errorf("execute tool %s: %w", call.Name, err)
	}

	return string(resp), nil
}

// ProcessToolResultMedia inspects a tool result for embedded media (base64 images,
// URL-based attachments) and extracts them as AgentAttachments.
// Returns (cleaned result JSON, extracted attachments).
func ProcessToolResultMedia(result string) (string, []pluginsdk.AgentAttachment) {
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		return result, nil
	}

	var attachments []pluginsdk.AgentAttachment

	// Check for inline base64 image data.
	imageData, hasImage := parsed["image_data"].(string)
	mimeType, hasMime := parsed["mime_type"].(string)
	if hasImage && hasMime && imageData != "" && mimeType != "" {
		attachments = append(attachments, pluginsdk.AgentAttachment{
			MimeType:  mimeType,
			ImageData: imageData,
		})
		parsed["image_data"] = "[image generated]"
	}

	// Check for URL-based attachments array.
	if rawAtts, ok := parsed["attachments"].([]interface{}); ok {
		for _, rawAtt := range rawAtts {
			att, ok := rawAtt.(map[string]interface{})
			if !ok {
				continue
			}
			attURL, _ := att["url"].(string)
			attMime, _ := att["mime_type"].(string)
			attType, _ := att["type"].(string)
			attFilename, _ := att["filename"].(string)
			if attURL != "" && attMime != "" {
				attachments = append(attachments, pluginsdk.AgentAttachment{
					MimeType: attMime,
					Type:     attType,
					URL:      attURL,
					Filename: attFilename,
				})
			}
		}
		if len(attachments) > 0 {
			parsed["attachments"] = "[media attached]"
		}
	}

	if len(attachments) == 0 {
		return result, nil
	}

	cleaned, err := json.Marshal(parsed)
	if err != nil {
		return result, attachments
	}

	return string(cleaned), attachments
}
