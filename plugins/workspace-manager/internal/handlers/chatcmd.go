package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/workspace-manager/internal/storage"
)

// ChatCommandWorkspaceList handles POST /chat-command/workspace/list.
func (h *Handler) ChatCommandWorkspaceList(c *gin.Context) {
	if h.sdk == nil {
		chatError(c, "Workspace manager not ready.")
		return
	}

	containers, err := h.sdk.ListManagedContainers()
	if err != nil {
		chatError(c, "Failed to fetch workspaces.")
		return
	}
	if len(containers) == 0 {
		chatText(c, "No workspaces found.")
		return
	}

	envNames := make(map[string]string)
	var fields []pluginsdk.EmbedField

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
			fmt.Sprintf("%s %s", workspaceStatusEmoji(mc.Status), mc.Status),
		}
		if envLabel != "" {
			lines = append(lines, fmt.Sprintf("Type: %s", envLabel))
		}
		if mc.Subdomain != "" && h.baseDomain != "" {
			lines = append(lines, fmt.Sprintf("URL: //%s.%s/", mc.Subdomain, h.baseDomain))
		}

		fields = append(fields, pluginsdk.EmbedField{
			Name:   mc.Name,
			Value:  strings.Join(lines, "\n"),
			Inline: true,
		})
	}

	c.JSON(http.StatusOK, pluginsdk.EmbedResponse(pluginsdk.EmbedContent{
		Title:  fmt.Sprintf("Workspaces (%d)", len(containers)),
		Color:  0x5865F2,
		Fields: fields,
	}))
}

// ChatCommandWorkspaceCreate handles POST /chat-command/workspace/create.
func (h *Handler) ChatCommandWorkspaceCreate(c *gin.Context) {
	if h.sdk == nil {
		chatError(c, "Workspace manager not ready.")
		return
	}

	var req struct {
		Params map[string]string `json:"params"`
	}
	c.ShouldBindJSON(&req)

	name := strings.TrimSpace(req.Params["name"])
	if name == "" {
		chatError(c, "Name is required.")
		return
	}

	envID := req.Params["environment"]
	if envID == "" {
		plugins, err := h.sdk.SearchPlugins("workspace:environment")
		if err != nil || len(plugins) == 0 {
			chatError(c, "No workspace environments available. Install an environment plugin first.")
			return
		}
		envID = plugins[0].ID
	}

	ws := h.fetchWorkspaceSchema(envID)
	if ws == nil {
		chatError(c, fmt.Sprintf("Unknown environment: %s", envID))
		return
	}

	wsKey := slugify(name)
	if wsKey == "" {
		chatError(c, "Name must contain at least one alphanumeric character.")
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
		chatError(c, "Failed to generate unique workspace ID.")
		return
	}

	ctx := c.Request.Context()

	// Ensure all declared disks exist via storage-disk API.
	var diskMounts []pluginsdk.DiskMount
	for _, spec := range ws.Disks {
		var diskName string
		if spec.Type == "workspace" {
			diskName = fmt.Sprintf("ws-%s-%s", wsID, wsKey)
		} else {
			diskName = spec.Name
		}

		diskID, _, err := h.ensureDisk(ctx, diskName, spec.Type)
		if err != nil {
			chatError(c, fmt.Sprintf("Failed to create disk %s: %v", diskName, err))
			return
		}
		diskMounts = append(diskMounts, pluginsdk.DiskMount{
			DiskID:   diskID,
			DiskType: spec.Type,
			Target:   spec.Target,
			ReadOnly: spec.ReadOnly,
		})
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
		Name:       name,
		Image:      ws.Image,
		Port:       ws.Port,
		Subdomain:  "ws-" + wsID,
		DiskMounts: diskMounts,
		Env:        env,
		Cmd:        cmd,
		DockerUser: ws.DockerUser,
	})
	if err != nil {
		chatError(c, "Failed to launch workspace: "+err.Error())
		return
	}

	h.db.Put(&storage.WorkspaceRecord{ContainerID: mc.ID, EnvironmentID: envID})
	h.emitEvent("workspace:created", fmt.Sprintf(`{"id":"%s","environment":"%s"}`, mc.ID, envID))

	url := ""
	if mc.Subdomain != "" && h.baseDomain != "" {
		url = fmt.Sprintf("//%s.%s/", mc.Subdomain, h.baseDomain)
	}

	lines := []string{
		"Status: starting",
		fmt.Sprintf("Type: %s", ws.DisplayName),
	}
	if url != "" {
		lines = append(lines, fmt.Sprintf("URL: %s", url))
	}

	c.JSON(http.StatusOK, pluginsdk.EmbedResponse(pluginsdk.EmbedContent{
		Title: "Workspace Created",
		Color: 0x57F287,
		Fields: []pluginsdk.EmbedField{
			{Name: name, Value: strings.Join(lines, "\n")},
		},
	}))
}

// ChatCommandWorkspaceRename handles POST /chat-command/workspace/rename.
// Disk stays linked by stable ID — only the display name changes.
func (h *Handler) ChatCommandWorkspaceRename(c *gin.Context) {
	if h.sdk == nil {
		chatError(c, "Workspace manager not ready.")
		return
	}

	var req struct {
		Params map[string]string `json:"params"`
	}
	c.ShouldBindJSON(&req)

	currentName := strings.TrimSpace(req.Params["workspace"])
	newName := strings.TrimSpace(req.Params["name"])
	if currentName == "" || newName == "" {
		chatError(c, "Both workspace and name are required.")
		return
	}

	containers, err := h.sdk.ListManagedContainers()
	if err != nil {
		chatError(c, "Failed to fetch workspaces.")
		return
	}

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
		chatError(c, fmt.Sprintf("No workspace found matching %q.", currentName))
		return
	}

	if _, err := h.sdk.UpdateManagedContainer(matched.ID, pluginsdk.UpdateManagedContainerRequest{
		Name: &newName,
	}); err != nil {
		chatError(c, "Failed to rename workspace: "+err.Error())
		return
	}

	h.emitEvent("workspace:renamed", fmt.Sprintf(`{"id":"%s","name":"%s"}`, matched.ID, newName))
	chatText(c, fmt.Sprintf("Renamed %s → %s", matched.Name, newName))
}

func chatText(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, pluginsdk.TextResponse(msg))
}

func chatError(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, pluginsdk.ErrorResponse(msg))
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
