package handlers

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-workspace-manager/internal/storage"
)

// DiscordCommandWorkspaceList handles POST /discord-command/workspace/list.
func (h *Handler) DiscordCommandWorkspaceList(c *gin.Context) {
	if h.sdk == nil {
		discordText(c, "Workspace manager not ready.")
		return
	}

	containers, err := h.sdk.ListManagedContainers()
	if err != nil {
		discordText(c, "Failed to fetch workspaces.")
		return
	}
	if len(containers) == 0 {
		discordText(c, "No workspaces found.")
		return
	}

	envNames := make(map[string]string)
	var fields []pluginsdk.DiscordEmbedFieldResponse

	for _, mc := range containers {
		envLabel := ""
		if rec, err := h.db.GetByContainerID(mc.ID); err == nil {
			envID := rec.EnvironmentID
			if name, ok := envNames[envID]; ok {
				envLabel = name
			} else if schema := h.fetchWorkspaceSchema(envID); schema != nil {
				envLabel = schema.DisplayName
				envNames[envID] = schema.DisplayName
			} else {
				envLabel = envID
			}
		}

		lines := []string{
			fmt.Sprintf("%s **%s**", workspaceStatusEmoji(mc.Status), mc.Status),
		}
		if envLabel != "" {
			lines = append(lines, fmt.Sprintf("**Type:** %s", envLabel))
		}
		if mc.Subdomain != "" && h.baseDomain != "" {
			lines = append(lines, fmt.Sprintf("**URL:** //%s.%s/", mc.Subdomain, h.baseDomain))
		}

		fields = append(fields, pluginsdk.DiscordEmbedFieldResponse{
			Name:   mc.Name,
			Value:  strings.Join(lines, "\n"),
			Inline: true,
		})
	}

	c.JSON(http.StatusOK, pluginsdk.DiscordCommandResponse{
		Type: "embed",
		Embeds: []pluginsdk.DiscordEmbedResponse{{
			Title:  fmt.Sprintf("Workspaces (%d)", len(containers)),
			Color:  0x5865F2,
			Fields: fields,
		}},
	})
}

// DiscordCommandWorkspaceCreate handles POST /discord-command/workspace/create.
func (h *Handler) DiscordCommandWorkspaceCreate(c *gin.Context) {
	if h.sdk == nil {
		discordText(c, "Workspace manager not ready.")
		return
	}

	var req struct {
		Name        string `json:"name"`
		Environment string `json:"environment"`
	}
	c.ShouldBindJSON(&req)

	name := strings.TrimSpace(req.Name)
	if name == "" {
		discordText(c, "Name is required.")
		return
	}

	// Resolve environment: use provided value or pick first available.
	envID := req.Environment
	if envID == "" {
		plugins, err := h.sdk.SearchPlugins("workspace:environment")
		if err != nil || len(plugins) == 0 {
			discordText(c, "No workspace environments available. Install an environment plugin first.")
			return
		}
		envID = plugins[0].ID
	}

	ws := h.fetchWorkspaceSchema(envID)
	if ws == nil {
		discordText(c, fmt.Sprintf("Unknown environment: %s", envID))
		return
	}

	// Delegate to the existing create logic by constructing a gin context is messy,
	// so call the SDK directly here.
	wsKey := slugify(name)
	if wsKey == "" {
		discordText(c, "Name must contain at least one alphanumeric character.")
		return
	}

	wsID := ""
	existingContainers, _ := h.sdk.ListManagedContainers()
	for attempts := 0; attempts < 10; attempts++ {
		candidate := randomID()
		subdCandidate := "ws-" + candidate
		collision := false
		for _, mc := range existingContainers {
			if mc.Subdomain == subdCandidate {
				collision = true
				break
			}
		}
		if !collision {
			wsID = candidate
			break
		}
	}
	if wsID == "" {
		discordText(c, "Failed to generate unique workspace ID.")
		return
	}

	volumeName := fmt.Sprintf("ws-%s-%s", wsID, wsKey)
	if err := os.MkdirAll(filepath.Join(h.workspaceDir, "volumes", volumeName), 0755); err != nil {
		discordText(c, "Failed to create workspace volume.")
		return
	}

	env := make(map[string]string)
	for k, v := range ws.EnvDefaults {
		env[k] = v
	}
	cmd := ws.Cmd
	if len(ws.ExtraCmdArgs) > 0 {
		cmd = append(append([]string{}, cmd...), ws.ExtraCmdArgs...)
	}

	mc, err := h.sdk.CreateManagedContainer(pluginsdk.CreateManagedContainerRequest{
		Name:        name,
		Image:       ws.Image,
		Port:        ws.Port,
		Subdomain:   "ws-" + wsID,
		VolumeName:  volumeName,
		ExtraMounts: ws.SharedMounts,
		Env:         env,
		Cmd:         cmd,
		DockerUser:  ws.DockerUser,
	})
	if err != nil {
		discordText(c, "Failed to launch workspace: "+err.Error())
		return
	}

	h.db.Put(&storage.WorkspaceRecord{ContainerID: mc.ID, EnvironmentID: envID})
	h.emitEvent("workspace:created", fmt.Sprintf(`{"id":"%s","environment":"%s"}`, mc.ID, envID))

	url := ""
	if mc.Subdomain != "" && h.baseDomain != "" {
		url = fmt.Sprintf("//%s.%s/", mc.Subdomain, h.baseDomain)
	}

	lines := []string{
		fmt.Sprintf("🟡 **starting**"),
		fmt.Sprintf("**Type:** %s", ws.DisplayName),
	}
	if url != "" {
		lines = append(lines, fmt.Sprintf("**URL:** %s", url))
	}

	c.JSON(http.StatusOK, pluginsdk.DiscordCommandResponse{
		Type: "embed",
		Embeds: []pluginsdk.DiscordEmbedResponse{{
			Title:  "Workspace Created",
			Color:  0x57F287, // green
			Fields: []pluginsdk.DiscordEmbedFieldResponse{
				{Name: name, Value: strings.Join(lines, "\n")},
			},
		}},
	})
}

// DiscordCommandWorkspaceRename handles POST /discord-command/workspace/rename.
func (h *Handler) DiscordCommandWorkspaceRename(c *gin.Context) {
	if h.sdk == nil {
		discordText(c, "Workspace manager not ready.")
		return
	}

	var req struct {
		Workspace string `json:"workspace"` // current name (matched by substring)
		Name      string `json:"name"`      // new display name
	}
	c.ShouldBindJSON(&req)

	currentName := strings.TrimSpace(req.Workspace)
	newName := strings.TrimSpace(req.Name)
	if currentName == "" || newName == "" {
		discordText(c, "Both workspace and name are required.")
		return
	}

	containers, err := h.sdk.ListManagedContainers()
	if err != nil {
		discordText(c, "Failed to fetch workspaces.")
		return
	}

	// Match by exact name first, then case-insensitive substring.
	var matched *pluginsdk.ManagedContainerInfo
	for i, mc := range containers {
		if mc.Name == currentName {
			matched = &containers[i]
			break
		}
	}
	if matched == nil {
		lower := strings.ToLower(currentName)
		for i, mc := range containers {
			if strings.Contains(strings.ToLower(mc.Name), lower) {
				matched = &containers[i]
				break
			}
		}
	}
	if matched == nil {
		discordText(c, fmt.Sprintf("No workspace found matching %q.", currentName))
		return
	}

	newSlug := slugify(newName)
	if newSlug == "" {
		discordText(c, "New name must contain at least one alphanumeric character.")
		return
	}

	wsPrefix := extractVolumePrefix(matched.VolumeName)
	newVolumeName := wsPrefix + newSlug

	if newVolumeName != matched.VolumeName {
		oldPath := filepath.Join(h.workspaceDir, "volumes", matched.VolumeName)
		newPath := filepath.Join(h.workspaceDir, "volumes", newVolumeName)
		if _, err := os.Stat(newPath); err == nil {
			discordText(c, "A volume with that name already exists.")
			return
		}
		if _, err := os.Stat(oldPath); err == nil {
			if err := os.Rename(oldPath, newPath); err != nil {
				discordText(c, "Failed to rename volume: "+err.Error())
				return
			}
		}
	}

	if newVolumeName != matched.VolumeName {
		if _, err := h.sdk.UpdateManagedContainer(matched.ID, pluginsdk.UpdateManagedContainerRequest{
			Name:       &newName,
			VolumeName: &newVolumeName,
		}); err != nil {
			// Rollback volume rename.
			os.Rename(
				filepath.Join(h.workspaceDir, "volumes", newVolumeName),
				filepath.Join(h.workspaceDir, "volumes", matched.VolumeName),
			)
			discordText(c, "Failed to update workspace: "+err.Error())
			return
		}
	} else {
		if _, err := h.sdk.UpdateManagedContainer(matched.ID, pluginsdk.UpdateManagedContainerRequest{
			Name: &newName,
		}); err != nil {
			discordText(c, "Failed to rename workspace: "+err.Error())
			return
		}
	}

	h.emitEvent("workspace:renamed", fmt.Sprintf(`{"id":"%s","name":"%s"}`, matched.ID, newName))

	c.JSON(http.StatusOK, pluginsdk.DiscordCommandResponse{
		Type:    "text",
		Content: fmt.Sprintf("Renamed **%s** → **%s**", matched.Name, newName),
	})
}

// discordText is a shorthand for a plain text DiscordCommandResponse.
func discordText(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, pluginsdk.DiscordCommandResponse{Type: "text", Content: msg})
}

func workspaceStatusEmoji(status string) string {
	switch status {
	case "running":
		return "🟢"
	case "stopped", "exited":
		return "⚫"
	case "starting":
		return "🟡"
	default:
		return "⚪"
	}
}
