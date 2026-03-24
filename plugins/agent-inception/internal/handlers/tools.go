package handlers

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
	"github.com/antimatter-studios/teamagentica/plugins/agent-inception/internal/inception"
)

// discoveredTool holds a tool schema from a plugin with its routing info.
type discoveredTool struct {
	PluginID     string          `json:"plugin_id"`
	Name         string          `json:"name"`
	PrefixedName string          `json:"prefixed_name"`
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

	if time.Since(globalToolCache.fetchedAt) < globalToolCache.ttl && globalToolCache.tools != nil {
		return globalToolCache.tools
	}

	plugins, err := sdk.SearchPlugins("tool:")
	if err != nil {
		log.Printf("agent-inception: tool discovery failed: %v", err)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var allTools []discoveredTool
	for _, p := range plugins {
		body, err := sdk.RouteToPlugin(ctx, p.ID, "GET", "/tools", nil)
		if err != nil {
			log.Printf("agent-inception: failed to get tools from %s: %v", p.ID, err)
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
			log.Printf("agent-inception: failed to parse tools from %s: %v", p.ID, err)
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
		log.Printf("agent-inception: discovered %d tools: %s", len(allTools), strings.Join(names, ", "))
	}

	return allTools
}

// buildToolDefs converts discovered tools into Inception ToolDef format.
func buildToolDefs(tools []discoveredTool) []inception.ToolDef {
	defs := make([]inception.ToolDef, len(tools))
	for i, t := range tools {
		defs[i] = inception.ToolDef{
			Type: "function",
			Function: inception.FunctionDef{
				Name:        t.PrefixedName,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		}
	}
	return defs
}

// executeToolCall runs a single tool call by routing through the kernel proxy.
func executeToolCall(sdk *pluginsdk.Client, tools []discoveredTool, call inception.ToolCall) (string, error) {
	parts := strings.SplitN(call.Function.Name, "__", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid tool name format: %s", call.Function.Name)
	}
	pluginID, toolName := parts[0], parts[1]

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
	if call.Function.Arguments != "" {
		body = bytes.NewReader([]byte(call.Function.Arguments))
	} else {
		body = bytes.NewReader([]byte("{}"))
	}

	resp, err := sdk.RouteToPlugin(ctx, pluginID, "POST", endpoint, body)
	if err != nil {
		return "", fmt.Errorf("execute tool %s: %w", call.Function.Name, err)
	}

	return string(resp), nil
}

// mediaAttachment represents an extracted media item from a tool result.
type mediaAttachment struct {
	MimeType  string `json:"mime_type"`
	ImageData string `json:"image_data,omitempty"`
	Type      string `json:"type,omitempty"`
	URL       string `json:"url,omitempty"`
	Filename  string `json:"filename,omitempty"`
}

// processToolResultMedia inspects a tool result for embedded media.
// Handles both base64 image_data and URL-based attachments (e.g. video URLs).
// Returns (cleanedResult, attachments extracted).
func processToolResultMedia(result string) (string, []mediaAttachment) {
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		return result, nil
	}

	var attachments []mediaAttachment

	// Check for inline base64 image data.
	imageData, hasImage := parsed["image_data"].(string)
	mimeType, hasMime := parsed["mime_type"].(string)
	if hasImage && hasMime && imageData != "" && mimeType != "" {
		attachments = append(attachments, mediaAttachment{
			MimeType:  mimeType,
			ImageData: imageData,
		})
		parsed["image_data"] = "[image generated]"
	}

	// Check for URL-based attachments array (e.g. from video generation tools).
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
				attachments = append(attachments, mediaAttachment{
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
