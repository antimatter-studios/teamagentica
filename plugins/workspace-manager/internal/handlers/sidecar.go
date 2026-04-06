package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"

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

	// Build shared_disks from workspace disk mounts so the sidecar
	// can access the workspace filesystem.
	var sharedDisks []pluginsdk.SharedDiskOverride
	for _, dm := range workspaceDiskMounts {
		sharedDisks = append(sharedDisks, pluginsdk.SharedDiskOverride{
			Name:   dm.DiskID,
			Type:   dm.DiskType,
			Target: dm.Target,
		})
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

	// Create alias in infra-alias-registry — use sidecarID as the alias
	// so it's human-readable: ws-{id}-{agent-plugin}.
	aliasName := sidecarID
	aliasPayload, _ := json.Marshal(map[string]string{
		"name":   aliasName,
		"type":   "agent",
		"plugin": sidecarID,
		"model":  agentModel,
	})
	if _, err := h.sdk.RouteToPlugin(ctx, "infra-alias-registry", "POST", "/aliases", bytes.NewReader(aliasPayload)); err != nil {
		log.Printf("sidecar: failed to create alias %s: %v", aliasName, err)
	}

	// Create persona for chat routing.
	personaPayload, _ := json.Marshal(map[string]interface{}{
		"alias":         aliasName,
		"system_prompt": fmt.Sprintf("You are an AI assistant for workspace %s. You have access to the workspace files.", subdomain),
		"backend_alias": aliasName,
		"model":         agentModel,
	})
	if _, err := h.sdk.RouteToPlugin(ctx, "infra-agent-persona", "POST", "/personas", bytes.NewReader(personaPayload)); err != nil {
		log.Printf("sidecar: failed to create persona %s: %v", aliasName, err)
	}

	log.Printf("sidecar: attached %s to workspace %s (alias=%s)", sidecarID, subdomain, aliasName)
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
