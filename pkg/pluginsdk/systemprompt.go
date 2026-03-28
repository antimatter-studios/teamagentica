package pluginsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
)

// PersonaInfo is a lightweight persona descriptor for system prompt preview.
type PersonaInfo struct {
	Alias        string `json:"alias"`
	SystemPrompt string `json:"system_prompt"`
	Model        string `json:"model"`
	BackendAlias string `json:"backend_alias"`
	Role         string `json:"role"`
	IsDefault    *bool  `json:"is_default"`
}

// FetchPersonas retrieves all personas from the persona plugin via the kernel proxy.
func (c *Client) FetchPersonas() ([]PersonaInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	data, err := c.RouteToPlugin(ctx, "infra-agent-persona", "GET", "/personas", nil)
	if err != nil {
		return nil, fmt.Errorf("fetch personas: %w", err)
	}

	var result struct {
		Personas []PersonaInfo `json:"personas"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		// Try unwrapping if it's a bare array.
		var personas []PersonaInfo
		if err2 := json.Unmarshal(data, &personas); err2 == nil {
			return personas, nil
		}
		return nil, fmt.Errorf("decode personas: %w", err)
	}
	return result.Personas, nil
}

// AliasPromptPreview describes one alias→persona mapping with the rendered prompt.
type AliasPromptPreview struct {
	Alias          string `json:"alias"`
	PersonaAlias   string `json:"persona_alias"`
	Model          string `json:"model"`
	Role           string `json:"role,omitempty"`
	IsDefault      bool   `json:"is_default"`
	RenderedPrompt string `json:"rendered_prompt"`
}

// fetchAliasesWithTypes retrieves aliases with their type field, mapping
// to capabilities so TargetFromInfo can classify them correctly.
func (c *Client) fetchAliasesWithTypes() ([]alias.AliasInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	data, err := c.RouteToPlugin(ctx, "infra-alias-registry", "GET", "/aliases", nil)
	if err != nil {
		return nil, fmt.Errorf("fetch aliases: %w", err)
	}

	var result struct {
		Aliases []struct {
			Name   string `json:"name"`
			Type   string `json:"type"`
			Plugin string `json:"plugin"`
			Model  string `json:"model"`
		} `json:"aliases"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode aliases: %w", err)
	}

	infos := make([]alias.AliasInfo, 0, len(result.Aliases))
	for _, a := range result.Aliases {
		target := a.Plugin
		if a.Model != "" {
			target = a.Plugin + ":" + a.Model
		}

		// Map alias type to capabilities for proper classification.
		var caps []string
		switch a.Type {
		case "agent":
			caps = []string{"agent:chat"}
		case "tool_agent":
			caps = []string{"agent:tool:image"}
		case "tool":
			caps = []string{"tool:mcp"}
		}

		infos = append(infos, alias.AliasInfo{
			Name:         a.Name,
			Target:       target,
			Capabilities: caps,
		})
	}
	return infos, nil
}

// SystemPromptPreview returns the rendered system prompts for every
// persona that routes to this plugin via an alias.
func (c *Client) SystemPromptPreview(pluginID, defaultPrompt string) ([]AliasPromptPreview, error) {
	// Fetch aliases and personas in parallel.
	type aliasResult struct {
		infos []alias.AliasInfo
		err   error
	}
	type personaResult struct {
		personas []PersonaInfo
		err      error
	}

	aliasC := make(chan aliasResult, 1)
	personaC := make(chan personaResult, 1)

	go func() {
		infos, err := c.fetchAliasesWithTypes()
		aliasC <- aliasResult{infos, err}
	}()
	go func() {
		personas, err := c.FetchPersonas()
		personaC <- personaResult{personas, err}
	}()

	ar := <-aliasC
	if ar.err != nil {
		return nil, fmt.Errorf("aliases: %w", ar.err)
	}
	pr := <-personaC
	if pr.err != nil {
		return nil, fmt.Errorf("personas: %w", pr.err)
	}

	// Build alias map for template rendering.
	aliasMap := alias.NewAliasMap(ar.infos)

	// Discover tools for template context.
	tools := discoverToolsForPreview(c)

	// Find aliases pointing to this plugin.
	myAliases := make(map[string]alias.AliasInfo) // alias name → info
	for _, info := range ar.infos {
		target := strings.TrimSpace(info.Target)
		pid := target
		if idx := strings.Index(pid, ":"); idx > 0 {
			pid = pid[:idx]
		}
		if pid == pluginID {
			myAliases[strings.ToLower(info.Name)] = info
		}
	}
	// Match personas to aliases.
	var previews []AliasPromptPreview
	for _, persona := range pr.personas {
		ba := strings.ToLower(strings.TrimSpace(persona.BackendAlias))
		if _, ok := myAliases[ba]; !ok {
			continue
		}

		// Render the system prompt template.
		promptTemplate := persona.SystemPrompt
		if promptTemplate == "" {
			promptTemplate = defaultPrompt
		}
		rendered := renderPromptTemplate(ba, promptTemplate, aliasMap, tools)

		isDefault := persona.IsDefault != nil && *persona.IsDefault
		model := persona.Model
		if model == "" {
			// Use model from alias target if persona doesn't override.
			if info, ok := myAliases[ba]; ok {
				target := strings.TrimSpace(info.Target)
				if idx := strings.Index(target, ":"); idx > 0 {
					model = target[idx+1:]
				}
			}
		}

		previews = append(previews, AliasPromptPreview{
			Alias:          ba,
			PersonaAlias:   persona.Alias,
			Model:          model,
			Role:           persona.Role,
			IsDefault:      isDefault,
			RenderedPrompt: rendered,
		})
	}

	sort.Slice(previews, func(i, j int) bool {
		return previews[i].Alias < previews[j].Alias
	})

	return previews, nil
}

// --- Template rendering (ported from relay discovery.go) ---

type previewAgent struct {
	Alias    string
	PluginID string
	Model    string
}

type previewSubTool struct {
	Name        string
	Description string
	Params      string
}

type previewAliasedTool struct {
	Alias    string
	PluginID string
	Model    string
	ToolType string
	SubTools []previewSubTool
}

type previewStorage struct {
	Alias       string
	PluginID    string
	StorageKind string
}

type previewMCPTool struct {
	Alias    string
	PluginID string
}

type previewAnonTool struct {
	FullName    string
	Description string
}

type previewPromptData struct {
	CurrentAlias string
	Alias        string // Synonym for CurrentAlias — templates may use {{.Alias}}.
	Agents       []previewAgent
	AliasedTools []previewAliasedTool
	Storage      []previewStorage
	MCPTools     []previewMCPTool
	AnonTools    []previewAnonTool
}

func renderPromptTemplate(currentAlias, personaPrompt string, aliases *alias.AliasMap, tools []alias.ToolInfo) string {
	entries := aliases.List()

	data := previewPromptData{CurrentAlias: currentAlias, Alias: currentAlias}

	aliasedPlugins := make(map[string]bool)
	for _, e := range entries {
		aliasedPlugins[e.Target.PluginID] = true
		switch e.Target.Type {
		case alias.TargetAgent:
			data.Agents = append(data.Agents, previewAgent{
				Alias:    e.Alias,
				PluginID: e.Target.PluginID,
				Model:    e.Target.Model,
			})
		case alias.TargetImage, alias.TargetVideo:
			toolType := "image generation"
			if e.Target.Type == alias.TargetVideo {
				toolType = "video generation"
			}
			at := previewAliasedTool{
				Alias:    e.Alias,
				PluginID: e.Target.PluginID,
				Model:    e.Target.Model,
				ToolType: toolType,
			}
			for _, t := range tools {
				if t.PluginID == e.Target.PluginID {
					at.SubTools = append(at.SubTools, previewSubTool{
						Name:        t.Name,
						Description: t.Description,
						Params:      previewToolParamSummary(t.Parameters),
					})
				}
			}
			data.AliasedTools = append(data.AliasedTools, at)
		case alias.TargetStorage:
			data.Storage = append(data.Storage, previewStorage{
				Alias:       e.Alias,
				PluginID:    e.Target.PluginID,
				StorageKind: previewStorageKind(e.Target.Capabilities),
			})
		case alias.TargetTool:
			data.MCPTools = append(data.MCPTools, previewMCPTool{
				Alias:    e.Alias,
				PluginID: e.Target.PluginID,
			})
		}
	}

	for _, t := range tools {
		if t.AliasName == "" && !aliasedPlugins[t.PluginID] {
			desc := t.Description
			if desc == "" {
				desc = t.PluginID
			}
			data.AnonTools = append(data.AnonTools, previewAnonTool{
				FullName:    t.FullName,
				Description: desc,
			})
		}
	}

	tmpl, err := template.New("prompt").Parse(personaPrompt)
	if err != nil {
		log.Printf("pluginsdk: failed to parse persona prompt template: %v", err)
		return personaPrompt
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		log.Printf("pluginsdk: failed to render persona prompt template: %v", err)
		return personaPrompt
	}
	return strings.TrimSpace(buf.String())
}

func previewStorageKind(capabilities []string) string {
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

func previewToolParamSummary(params map[string]interface{}) string {
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

// discoverToolsForPreview fetches tool schemas from all tool:* plugins.
func discoverToolsForPreview(sdk *Client) []alias.ToolInfo {
	if sdk == nil {
		return nil
	}

	plugins, err := sdk.SearchPlugins("tool:")
	if err != nil {
		log.Printf("pluginsdk: tool discovery for preview failed: %v", err)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var allTools []alias.ToolInfo
	for _, p := range plugins {
		body, err := sdk.RouteToPlugin(ctx, p.ID, "GET", "/mcp", nil)
		if err != nil {
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

	return allTools
}
