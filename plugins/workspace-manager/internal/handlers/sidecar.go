package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

// attachSidecar creates and starts an agent sidecar plugin for a workspace,
// then registers an alias and persona so it's routable via @ws-{subdomain}.
func (h *Handler) attachSidecar(ctx context.Context, containerID, subdomain, agentPlugin, agentModel string, workspaceDiskMounts []pluginsdk.DiskMount) (string, error) {
	if agentPlugin == "" {
		return "", nil
	}

	sidecarID := subdomain + "-" + agentPlugin

	// Look up the base agent plugin to get its image.
	// The kernel wraps the response: {"plugin": {…}}, so unwrap first.
	baseResp, err := h.sdk.GetPlugin(agentPlugin)
	if err != nil {
		return "", fmt.Errorf("failed to look up agent plugin %s: %w", agentPlugin, err)
	}
	basePlugin, _ := baseResp["plugin"].(map[string]interface{})
	if basePlugin == nil {
		basePlugin = baseResp // fallback if format changes
	}
	image, _ := basePlugin["image"].(string)
	if image == "" {
		return "", fmt.Errorf("agent plugin %s has no image", agentPlugin)
	}

	// Create the sidecar plugin in the kernel.
	err = h.sdk.CreatePlugin(pluginsdk.CreatePluginRequest{
		ID:           sidecarID,
		Name:         fmt.Sprintf("Workspace %s Agent (%s)", subdomain, agentPlugin),
		Version:      "sidecar",
		Image:        image,
		HTTPPort:     8082,
		Capabilities: []string{"agent:chat"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create sidecar plugin: %w", err)
	}

	// Build shared_disks for the sidecar:
	// 1. Base agent's shared disks (CLI auth/config)
	// 2. Workspace's primary disk (type=workspace) so the agent can access files
	var sharedDisks []pluginsdk.SharedDiskOverride

	// Agent's shared disks (e.g. agent-claude → /home/coder/.claude for CLI auth).
	if rawDisks, ok := basePlugin["shared_disks"]; ok {
		if diskList, ok := rawDisks.([]interface{}); ok {
			for _, d := range diskList {
				if dm, ok := d.(map[string]interface{}); ok {
					name, _ := dm["name"].(string)
					diskType, _ := dm["type"].(string)
					target, _ := dm["target"].(string)
					if name == "" || target == "" {
						continue
					}
					if diskType == "" {
						diskType = "shared"
					}
					sharedDisks = append(sharedDisks, pluginsdk.SharedDiskOverride{
						Name:   name,
						Type:   diskType,
						Target: target,
					})
				}
			}
		}
	}

	// Workspace's primary disk only (type=workspace) — gives agent access to project files.
	// Resolve disk ID → disk name via storage-disk so we use the human-readable name,
	// not the UUID (which would cause resolveDiskPaths to create a spurious disk).
	for _, dm := range workspaceDiskMounts {
		if dm.DiskType == "workspace" {
			diskName := dm.DiskID // fallback to ID
			if data, err := h.sdk.RouteToPlugin(ctx, "storage-disk", "GET", "/disks/by-id/"+dm.DiskID, nil); err == nil {
				var info struct{ Name string `json:"name"` }
				if json.Unmarshal(data, &info) == nil && info.Name != "" {
					diskName = info.Name
				}
			}
			sharedDisks = append(sharedDisks, pluginsdk.SharedDiskOverride{
				Name:   diskName,
				Type:   dm.DiskType,
				Target: dm.Target,
			})
			break
		}
	}

	// Copy config from the base agent plugin so the sidecar has API keys etc.
	if err := h.sdk.CopyPluginConfig(agentPlugin, sidecarID); err != nil {
		log.Printf("sidecar: failed to copy config from %s to %s: %v", agentPlugin, sidecarID, err)
	}

	// Configure the sidecar for remote execution: Claude CLI runs inside the
	// workspace container, the sidecar proxies via WebSocket.
	execWSURL := fmt.Sprintf("ws://teamagentica-mc-%s:9100/exec", containerID)
	if err := h.sdk.SetPluginConfig(sidecarID, map[string]string{
		"CLAUDE_EXEC_MODE":   "remote",
		"CLAUDE_EXEC_WS_URL": execWSURL,
	}); err != nil {
		log.Printf("sidecar: failed to set remote exec config: %v", err)
	}

	// Enable the sidecar plugin with workspace disk overrides.
	err = h.sdk.EnablePlugin(sidecarID, &pluginsdk.EnablePluginRequest{
		SharedDisks: sharedDisks,
	})
	if err != nil {
		// Clean up on failure.
		h.sdk.DeletePlugin(sidecarID)
		return "", fmt.Errorf("failed to enable sidecar plugin: %w", err)
	}

	// Register alias and persona for chat routing.
	h.ensureAliasAndPersona(ctx, subdomain, sidecarID, agentModel)

	log.Printf("sidecar: attached %s to workspace %s (alias=%s)", sidecarID, subdomain, sidecarID)
	return sidecarID, nil
}

// detachSidecar removes the alias, persona, and sidecar plugin for a workspace.
func (h *Handler) detachSidecar(ctx context.Context, subdomain, sidecarID string) {
	if sidecarID == "" {
		return
	}

	aliasName := sidecarID // alias = sidecarID = ws-{id}-{agent-plugin}

	// Delete persona.
	if _, err := h.sdk.RouteToPlugin(ctx, "infra-agent-persona", "DELETE", "/personas/"+aliasName, nil); err != nil {
		log.Printf("sidecar: failed to delete persona %s: %v", aliasName, err)
	}

	// Delete alias.
	if _, err := h.sdk.RouteToPlugin(ctx, "infra-alias-registry", "DELETE", "/aliases/"+aliasName, nil); err != nil {
		log.Printf("sidecar: failed to delete alias %s: %v", aliasName, err)
	}

	// Disable and delete the sidecar plugin.
	if err := h.sdk.DisablePlugin(sidecarID); err != nil {
		log.Printf("sidecar: failed to disable %s: %v", sidecarID, err)
	}
	if err := h.sdk.DeletePlugin(sidecarID); err != nil {
		log.Printf("sidecar: failed to delete %s: %v", sidecarID, err)
	}

	log.Printf("sidecar: detached %s (alias=%s)", sidecarID, aliasName)
}

// ensureAliasAndPersona creates the alias and persona for a sidecar.
// If they already exist (409), deletes and re-creates to ensure correct config.
func (h *Handler) ensureAliasAndPersona(ctx context.Context, subdomain, sidecarID, agentModel string) {
	aliasName := sidecarID
	aliasPayload, _ := json.Marshal(map[string]string{
		"name":   aliasName,
		"type":   "agent",
		"plugin": sidecarID,
		"model":  agentModel,
	})
	if _, err := h.sdk.RouteToPlugin(ctx, "infra-alias-registry", "POST", "/aliases", bytes.NewReader(aliasPayload)); err != nil {
		if strings.Contains(err.Error(), "409") {
			// Already exists — delete and re-create to update config.
			h.sdk.RouteToPlugin(ctx, "infra-alias-registry", "DELETE", "/aliases/"+aliasName, nil)
			h.sdk.RouteToPlugin(ctx, "infra-alias-registry", "POST", "/aliases", bytes.NewReader(aliasPayload))
		} else {
			log.Printf("sidecar: failed to ensure alias %s: %v", aliasName, err)
		}
	}

	personaPayload, _ := json.Marshal(map[string]interface{}{
		"alias":         aliasName,
		"system_prompt": fmt.Sprintf("You are an AI assistant for workspace %s. You have access to the workspace files.", subdomain),
		"backend_alias": aliasName,
		"model":         agentModel,
	})
	if _, err := h.sdk.RouteToPlugin(ctx, "infra-agent-persona", "POST", "/personas", bytes.NewReader(personaPayload)); err != nil {
		if strings.Contains(err.Error(), "409") {
			h.sdk.RouteToPlugin(ctx, "infra-agent-persona", "DELETE", "/personas/"+aliasName, nil)
			h.sdk.RouteToPlugin(ctx, "infra-agent-persona", "POST", "/personas", bytes.NewReader(personaPayload))
		} else {
			log.Printf("sidecar: failed to ensure persona %s: %v", aliasName, err)
		}
	}
}

// ReconcileSidecars ensures aliases and personas exist for all workspaces that
// have an attached sidecar, and cleans up stale aliases for sidecars that no
// longer exist. Called on startup to recover state after a kernel restart.
func (h *Handler) ReconcileSidecars(ctx context.Context) {
	opts, err := h.db.ListAllOptions()
	if err != nil {
		log.Printf("sidecar: reconcile failed to list options: %v", err)
		return
	}

	// Build set of live kernel container IDs to detect orphaned options.
	containers, _ := h.sdk.ListManagedContainers()
	liveContainerIDs := make(map[string]bool, len(containers))
	for _, mc := range containers {
		liveContainerIDs[mc.ID] = true
	}

	// Build set of active sidecar IDs (only for workspaces that still exist).
	activeSidecars := make(map[string]bool)

	var count int
	for _, opt := range opts {
		if opt.SidecarID == "" || opt.AgentPlugin == "" {
			continue
		}
		// Skip options for workspaces that no longer exist in the kernel.
		if !liveContainerIDs[opt.ContainerID] {
			continue
		}
		activeSidecars[opt.SidecarID] = true
		subdomain := strings.TrimSuffix(opt.SidecarID, "-"+opt.AgentPlugin)
		if subdomain == "" || subdomain == opt.SidecarID {
			continue
		}
		h.ensureAliasAndPersona(ctx, subdomain, opt.SidecarID, opt.AgentModel)
		count++
	}
	if count > 0 {
		log.Printf("sidecar: reconciled %d workspace aliases", count)
	}

	// Clean up stale workspace aliases that no longer have an active sidecar.
	if aliasData, err := h.sdk.RouteToPlugin(ctx, "infra-alias-registry", "GET", "/aliases", nil); err == nil {
		var resp struct {
			Aliases []struct {
				Name string `json:"name"`
			} `json:"aliases"`
		}
		if json.Unmarshal(aliasData, &resp) == nil {
			for _, a := range resp.Aliases {
				if !strings.HasPrefix(a.Name, "ws-") {
					continue
				}
				if activeSidecars[a.Name] {
					continue
				}
				// Stale workspace alias — clean up.
				h.sdk.RouteToPlugin(ctx, "infra-alias-registry", "DELETE", "/aliases/"+a.Name, nil)
				h.sdk.RouteToPlugin(ctx, "infra-agent-persona", "DELETE", "/personas/"+a.Name, nil)
				log.Printf("sidecar: cleaned up stale alias %s", a.Name)
			}
		}
	}
}
