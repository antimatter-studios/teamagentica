package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
)

// workerTemplateData is the data passed when rendering a worker persona's system prompt.
type workerTemplateData struct {
	Alias        string
	IsSelfWorker bool
}

// getSelfWorkerPrompt looks up the worker role's system prompt from the persona plugin
// and renders it with IsSelfWorker=true. Falls back to a basic prompt if unavailable.
func (r *relay) getSelfWorkerPrompt(coordAlias string) string {
	if r.sdk != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		body, err := r.sdk.RouteToPlugin(ctx, "infra-agent-persona", "GET", "/roles/worker", nil)
		if err == nil {
			var role struct {
				SystemPrompt string `json:"system_prompt"`
			}
			if json.Unmarshal(body, &role) == nil && role.SystemPrompt != "" {
				return renderWorkerPrompt(role.SystemPrompt, workerTemplateData{
					Alias:        coordAlias,
					IsSelfWorker: true,
				})
			}
		}
	}
	// Fallback if persona plugin is unavailable.
	return "You have been delegated a task by the coordinator. Respond with your answer in plain text or Markdown. NEVER output a JSON task plan."
}

// renderWorkerPrompt renders a worker persona's system prompt as a Go template.
// If the prompt contains no template syntax, it's returned as-is.
func renderWorkerPrompt(promptTemplate string, data workerTemplateData) string {
	tmpl, err := template.New("worker").Parse(promptTemplate)
	if err != nil {
		return promptTemplate
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return promptTemplate
	}
	return strings.TrimSpace(buf.String())
}

// toolDiscovery caches discovered tools from tool:* plugins.
type toolDiscovery struct {
	mu        sync.RWMutex
	tools     []alias.ToolInfo
	fetchedAt time.Time
	ttl       time.Duration
}

var globalToolDiscovery = &toolDiscovery{ttl: 60 * time.Second}

// discoverTools queries the kernel for tool:* plugins and returns their tool info.
func discoverTools(sdk *pluginsdk.Client) []alias.ToolInfo {
	if sdk == nil {
		return nil
	}

	globalToolDiscovery.mu.RLock()
	if time.Since(globalToolDiscovery.fetchedAt) < globalToolDiscovery.ttl && globalToolDiscovery.tools != nil {
		tools := globalToolDiscovery.tools
		globalToolDiscovery.mu.RUnlock()
		return tools
	}
	globalToolDiscovery.mu.RUnlock()

	globalToolDiscovery.mu.Lock()
	defer globalToolDiscovery.mu.Unlock()

	if time.Since(globalToolDiscovery.fetchedAt) < globalToolDiscovery.ttl && globalToolDiscovery.tools != nil {
		return globalToolDiscovery.tools
	}

	plugins, err := sdk.SearchPlugins("tool:")
	if err != nil {
		log.Printf("relay: tool discovery failed: %v", err)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var allTools []alias.ToolInfo
	for _, p := range plugins {
		body, err := sdk.RouteToPlugin(ctx, p.ID, "GET", "/mcp", nil)
		if err != nil {
			log.Printf("relay: failed to get tools from %s: %v", p.ID, err)
			continue
		}

		var resp struct {
			Tools []struct {
				Name        string                 `json:"name"`
				Description string                 `json:"description"`
				Endpoint    string                 `json:"endpoint"`
				Parameters  map[string]interface{} `json:"parameters"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			log.Printf("relay: failed to parse tools from %s: %v", p.ID, err)
			continue
		}

		for _, t := range resp.Tools {
			allTools = append(allTools, alias.ToolInfo{
				FullName:    p.ID + "__" + t.Name,
				Name:        t.Name,
				Description: t.Description,
				PluginID:    p.ID,
				Endpoint:    t.Endpoint,
				Parameters:  t.Parameters,
			})
		}
	}

	globalToolDiscovery.tools = allTools
	globalToolDiscovery.fetchedAt = time.Now()

	if len(allTools) > 0 {
		log.Printf("relay: discovered %d tools from %d plugins", len(allTools), len(plugins))
	}

	return allTools
}

// --- Template data types ---

type tmplAgent struct {
	Alias    string
	PluginID string
	Model    string
}

type tmplSubTool struct {
	Name        string
	Description string
	Params      string // compact param summary
}

type tmplAliasedTool struct {
	Alias    string
	PluginID string
	Model    string
	ToolType string // "image generation" or "video generation"
	SubTools []tmplSubTool
}

type tmplStorage struct {
	Alias       string
	PluginID    string
	StorageKind string
}

type tmplMCPTool struct {
	Alias    string
	PluginID string
}

type tmplAnonTool struct {
	FullName    string
	Description string
}

type coordinatorContextData struct {
	Agents []tmplAgent
	AliasedTools  []tmplAliasedTool
	Storage       []tmplStorage
	MCPTools      []tmplMCPTool
	AnonTools     []tmplAnonTool
}

// buildCoordinatorContext renders the coordinator system prompt from the template,
// populated with persona prompt, alias, and tool discovery data.
func buildCoordinatorContext(personaPrompt string, aliases *alias.AliasMap, discoveredTools []alias.ToolInfo) string {
	entries := aliases.List()

	data := coordinatorContextData{}

	// Classify aliases.
	aliasedPlugins := make(map[string]bool)
	for _, e := range entries {
		aliasedPlugins[e.Target.PluginID] = true
		switch e.Target.Type {
		case alias.TargetAgent:
			data.Agents = append(data.Agents, tmplAgent{
				Alias:    e.Alias,
				PluginID: e.Target.PluginID,
				Model:    e.Target.Model,
			})
		case alias.TargetImage, alias.TargetVideo:
			toolType := "image generation"
			if e.Target.Type == alias.TargetVideo {
				toolType = "video generation"
			}
			at := tmplAliasedTool{
				Alias:    e.Alias,
				PluginID: e.Target.PluginID,
				Model:    e.Target.Model,
				ToolType: toolType,
			}
			// Attach sub-tools from discovery.
			for _, t := range discoveredTools {
				if t.PluginID == e.Target.PluginID {
					at.SubTools = append(at.SubTools, tmplSubTool{
						Name:        t.Name,
						Description: t.Description,
						Params:      toolParamSummary(t.Parameters),
					})
				}
			}
			data.AliasedTools = append(data.AliasedTools, at)
		case alias.TargetStorage:
			data.Storage = append(data.Storage, tmplStorage{
				Alias:       e.Alias,
				PluginID:    e.Target.PluginID,
				StorageKind: storageKindFromCapabilities(e.Target.Capabilities),
			})
		case alias.TargetTool:
			data.MCPTools = append(data.MCPTools, tmplMCPTool{
				Alias:    e.Alias,
				PluginID: e.Target.PluginID,
			})
		}
	}

	// Anonymous tools (not covered by any alias).
	for _, t := range discoveredTools {
		if t.AliasName == "" && !aliasedPlugins[t.PluginID] {
			desc := t.Description
			if desc == "" {
				desc = t.PluginID
			}
			data.AnonTools = append(data.AnonTools, tmplAnonTool{
				FullName:    t.FullName,
				Description: desc,
			})
		}
	}

	// Parse the persona prompt as a Go template — it contains {{.Agents}}, {{.Tools}}, etc.
	tmpl, err := template.New("coordinator").Parse(personaPrompt)
	if err != nil {
		log.Printf("relay: failed to parse coordinator prompt as template: %v", err)
		return personaPrompt // Fall back to raw prompt if template syntax is invalid.
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		log.Printf("relay: failed to render coordinator context template: %v", err)
		return personaPrompt
	}
	return strings.TrimSpace(buf.String())
}

// storageKindFromCapabilities returns a human-readable storage type label.
func storageKindFromCapabilities(capabilities []string) string {
	for _, cap := range capabilities {
		if cap == "storage:disk" || cap == "tool:storage:disk" {
			return "disk storage"
		}
		if cap == "storage:object" || cap == "tool:storage:object" {
			return "object storage"
		}
	}
	return "file storage"
}

// toolParamSummary returns a compact description of params from a JSON Schema.
func toolParamSummary(params map[string]interface{}) string {
	if params == nil {
		return ""
	}
	props, _ := params["properties"].(map[string]interface{})
	if len(props) == 0 {
		return ""
	}
	reqSlice, _ := params["required"].([]interface{})
	required := make(map[string]bool)
	for _, r := range reqSlice {
		if s, ok := r.(string); ok {
			required[s] = true
		}
	}
	var parts []string
	for name := range props {
		label := name
		if required[name] {
			label += " (required)"
		}
		parts = append(parts, label)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// enrichSystemPrompt renders the full coordinator system prompt by combining
// the persona prompt with alias/tool context via the template.
func (r *relay) enrichSystemPrompt(personaPrompt string, aliases *alias.AliasMap) string {
	tools := discoverTools(r.sdk)
	return buildCoordinatorContext(personaPrompt, aliases, tools)
}
