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
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/agent-gemini/internal/gemini"
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

	// Double-check after acquiring write lock.
	if time.Since(globalToolCache.fetchedAt) < globalToolCache.ttl && globalToolCache.tools != nil {
		return globalToolCache.tools
	}

	plugins, err := sdk.SearchPlugins("tool:")
	if err != nil {
		log.Printf("agent-gemini: tool discovery failed: %v", err)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var allTools []discoveredTool
	for _, p := range plugins {
		body, err := sdk.RouteToPlugin(ctx, p.ID, "GET", "/tools", nil)
		if err != nil {
			log.Printf("agent-gemini: failed to get tools from %s: %v", p.ID, err)
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
			log.Printf("agent-gemini: failed to parse tools from %s: %v", p.ID, err)
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
		log.Printf("agent-gemini: discovered %d tools: %s", len(allTools), strings.Join(names, ", "))
	}

	return allTools
}

// buildToolDefs converts discovered tools into Gemini FunctionDeclaration format.
func buildToolDefs(tools []discoveredTool) []gemini.FunctionDeclaration {
	defs := make([]gemini.FunctionDeclaration, len(tools))
	for i, t := range tools {
		defs[i] = gemini.FunctionDeclaration{
			Name:        t.PrefixedName,
			Description: t.Description,
			Parameters:  t.Parameters,
		}
	}
	return defs
}

// executeToolCall runs a single tool call by routing through the kernel proxy.
func executeToolCall(sdk *pluginsdk.Client, tools []discoveredTool, funcName string, args map[string]interface{}) (string, error) {
	parts := strings.SplitN(funcName, "__", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid tool name format: %s", funcName)
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

	argsJSON, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("marshal args: %w", err)
	}

	resp, err := sdk.RouteToPlugin(ctx, pluginID, "POST", endpoint, bytes.NewReader(argsJSON))
	if err != nil {
		return "", fmt.Errorf("execute tool %s: %w", funcName, err)
	}

	return string(resp), nil
}

// buildSystemPrompt generates the agent's system prompt based on its role and capabilities.
func buildSystemPrompt(sdk *pluginsdk.Client, isCoordinator bool, agentAlias string, tools []discoveredTool) string {
	var sb strings.Builder

	if isCoordinator {
		sb.WriteString("You are the coordinator agent. You can answer questions directly or delegate to specialized agents and tools.\n\n")

		if sdk != nil {
			aliases, err := sdk.FetchAliases()
			if err == nil && len(aliases) > 0 {
				aliasMap := alias.NewAliasMap(aliases)
				entries := aliasMap.List()

				var agents, aliasedTools, storageAliases []alias.AliasEntry
				for _, e := range entries {
					switch e.Target.Type {
					case alias.TargetAgent:
						agents = append(agents, e)
					case alias.TargetImage, alias.TargetVideo:
						aliasedTools = append(aliasedTools, e)
					case alias.TargetStorage:
						storageAliases = append(storageAliases, e)
					}
				}

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
					sb.WriteString("AVAILABLE TOOL ALIASES:\n")
					for _, e := range aliasedTools {
						toolType := "image generation"
						if e.Target.Type == alias.TargetVideo {
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

				if len(storageAliases) > 0 {
					sb.WriteString("AVAILABLE STORAGE:\n")
					for _, e := range storageAliases {
						sb.WriteString(fmt.Sprintf("- @%s → file storage via %s\n", e.Alias, e.Target.PluginID))
					}
					sb.WriteString("\n")
				}
			}
		}

		sb.WriteString("ROUTING INSTRUCTIONS:\n")
		sb.WriteString("- To delegate to an agent or tool, respond with EXACTLY:\nROUTE:@alias\nmessage to send\n")
		sb.WriteString("- For image/video requests, delegate to the appropriate tool alias.\n")
		sb.WriteString("- If you can answer directly, just respond normally.\n\n")
	} else if agentAlias != "" {
		sb.WriteString(fmt.Sprintf("You are @%s, an AI assistant in a multi-agent platform.\n\n", agentAlias))
	} else {
		sb.WriteString("You are an AI assistant in a multi-agent platform.\n\n")
	}

	if len(tools) > 0 {
		sb.WriteString("AVAILABLE TOOLS (function calling):\n")
		sb.WriteString("You have access to the following tools that you can invoke via function calling:\n")
		for _, t := range tools {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", t.PrefixedName, t.Description))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// mediaAttachment represents an extracted image from a tool result.
type mediaAttachment struct {
	MimeType  string `json:"mime_type"`
	ImageData string `json:"image_data"`
}

// processToolResultMedia inspects a tool result for embedded image data.
// If the JSON contains "image_data" + "mime_type" fields, it extracts them
// and replaces the base64 blob with a short placeholder so the model gets a clean summary.
// Returns (cleanedResult, attachment or nil).
func processToolResultMedia(result string) (string, *mediaAttachment) {
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		return result, nil
	}

	imageData, hasImage := parsed["image_data"].(string)
	mimeType, hasMime := parsed["mime_type"].(string)
	if !hasImage || !hasMime || imageData == "" || mimeType == "" {
		return result, nil
	}

	// Replace the base64 blob so the model sees a short summary instead.
	parsed["image_data"] = "[image generated]"
	cleaned, err := json.Marshal(parsed)
	if err != nil {
		return result, nil
	}

	return string(cleaned), &mediaAttachment{
		MimeType:  mimeType,
		ImageData: imageData,
	}
}
