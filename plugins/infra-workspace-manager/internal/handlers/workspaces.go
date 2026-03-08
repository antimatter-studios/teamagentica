package handlers

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

type Handler struct {
	workspaceDir string
	debug        bool
	sdk          *pluginsdk.Client
}

func NewHandler(workspaceDir string, debug bool) *Handler {
	return &Handler{
		workspaceDir: workspaceDir,
		debug:        debug,
	}
}

func (h *Handler) SetSDK(sdk *pluginsdk.Client) {
	h.sdk = sdk
}

func (h *Handler) emitEvent(eventType, detail string) {
	if h.sdk != nil {
		h.sdk.ReportEvent(eventType, detail)
	}
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"plugin":  "infra-workspace-manager",
		"version": "1.0.0",
	})
}

type workspaceInfo struct {
	ID         string `json:"id"`
	GitBranch  string `json:"git_branch,omitempty"`
	GitDirty   bool   `json:"git_dirty"`
	HasSession bool   `json:"has_session"`
	SizeBytes  int64  `json:"size_bytes"`
}

func (h *Handler) ListWorkspaces(c *gin.Context) {
	entries, err := os.ReadDir(h.workspaceDir)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusOK, gin.H{"workspaces": []workspaceInfo{}})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var workspaces []workspaceInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		wsPath := filepath.Join(h.workspaceDir, e.Name())
		info := workspaceInfo{ID: e.Name()}

		// Git branch.
		if _, err := os.Stat(filepath.Join(wsPath, ".git")); err == nil {
			cmd := exec.Command("git", "branch", "--show-current")
			cmd.Dir = wsPath
			if out, err := cmd.Output(); err == nil {
				info.GitBranch = strings.TrimSpace(string(out))
			}
			cmd2 := exec.Command("git", "status", "--porcelain")
			cmd2.Dir = wsPath
			if out, err := cmd2.Output(); err == nil {
				info.GitDirty = strings.TrimSpace(string(out)) != ""
			}
		}

		// Session state.
		if _, err := os.Stat(filepath.Join(wsPath, ".claude-config")); err == nil {
			info.HasSession = true
		}

		// Size.
		info.SizeBytes = dirSize(wsPath)

		workspaces = append(workspaces, info)
	}

	if workspaces == nil {
		workspaces = []workspaceInfo{}
	}
	c.JSON(http.StatusOK, gin.H{"workspaces": workspaces})
}

func (h *Handler) CreateWorkspace(c *gin.Context) {
	var req struct {
		ID      string `json:"id"`
		GitRepo string `json:"git_repo,omitempty"`
		GitRef  string `json:"git_ref,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace id is required"})
		return
	}
	if !isValidWorkspaceID(req.ID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workspace id: must be alphanumeric, hyphens, or underscores, max 128 chars"})
		return
	}

	wsPath := filepath.Join(h.workspaceDir, req.ID)
	if _, err := os.Stat(wsPath); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "workspace already exists"})
		return
	}

	if err := os.MkdirAll(wsPath, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create workspace: " + err.Error()})
		return
	}

	h.emitEvent("workspace:created", fmt.Sprintf(`{"id":"%s"}`, req.ID))

	if req.GitRepo != "" {
		args := []string{"clone", req.GitRepo, "."}
		cmd := exec.CommandContext(c.Request.Context(), "git", args...)
		cmd.Dir = wsPath

		output, err := cmd.CombinedOutput()
		if err != nil {
			os.RemoveAll(wsPath)
			c.JSON(http.StatusBadGateway, gin.H{"error": "git clone failed: " + string(output)})
			return
		}

		if req.GitRef != "" {
			checkout := exec.CommandContext(c.Request.Context(), "git", "checkout", req.GitRef)
			checkout.Dir = wsPath
			checkout.CombinedOutput()
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":   req.ID,
		"path": wsPath,
	})
}

func (h *Handler) WorkspaceStatus(c *gin.Context) {
	id := c.Param("id")
	if !isValidWorkspaceID(id) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workspace id"})
		return
	}

	wsPath := filepath.Join(h.workspaceDir, id)
	info, err := os.Stat(wsPath)
	if os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
		return
	}

	result := gin.H{
		"id":         id,
		"path":       wsPath,
		"created_at": info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
	}

	if _, err := os.Stat(filepath.Join(wsPath, ".git")); err == nil {
		cmd := exec.Command("git", "log", "--oneline", "-5")
		cmd.Dir = wsPath
		if out, err := cmd.Output(); err == nil {
			result["git_log"] = strings.TrimSpace(string(out))
		}
		cmd2 := exec.Command("git", "branch", "--show-current")
		cmd2.Dir = wsPath
		if out, err := cmd2.Output(); err == nil {
			result["git_branch"] = strings.TrimSpace(string(out))
		}
		cmd3 := exec.Command("git", "status", "--porcelain")
		cmd3.Dir = wsPath
		if out, err := cmd3.Output(); err == nil {
			changes := strings.TrimSpace(string(out))
			result["git_dirty"] = changes != ""
			if changes != "" {
				result["git_changes"] = changes
			}
		}
	}

	// Session dirs.
	var sessions []string
	entries, _ := os.ReadDir(wsPath)
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), ".claude-config") {
			sessions = append(sessions, e.Name())
		}
	}
	result["sessions"] = sessions

	c.JSON(http.StatusOK, result)
}

func (h *Handler) DeleteWorkspace(c *gin.Context) {
	id := c.Param("id")
	if !isValidWorkspaceID(id) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workspace id"})
		return
	}

	wsPath := filepath.Join(h.workspaceDir, id)
	if _, err := os.Stat(wsPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
		return
	}

	if err := os.RemoveAll(wsPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete workspace: " + err.Error()})
		return
	}

	h.emitEvent("workspace:deleted", fmt.Sprintf(`{"id":"%s"}`, id))
	c.JSON(http.StatusOK, gin.H{"message": "workspace deleted", "id": id})
}

func (h *Handler) PersistWorkspace(c *gin.Context) {
	id := c.Param("id")
	if !isValidWorkspaceID(id) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workspace id"})
		return
	}

	var req struct {
		CommitMessage string `json:"commit_message,omitempty"`
		Push          bool   `json:"push"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	wsPath := filepath.Join(h.workspaceDir, id)
	if _, err := os.Stat(filepath.Join(wsPath, ".git")); os.IsNotExist(err) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace is not a git repository"})
		return
	}

	ctx := c.Request.Context()

	addCmd := exec.CommandContext(ctx, "git", "add", "-A")
	addCmd.Dir = wsPath
	if out, err := addCmd.CombinedOutput(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "git add failed: " + string(out)})
		return
	}

	diffCmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--quiet")
	diffCmd.Dir = wsPath
	if diffCmd.Run() == nil {
		c.JSON(http.StatusOK, gin.H{"message": "nothing to commit", "pushed": false})
		return
	}

	msg := req.CommitMessage
	if msg == "" {
		msg = "workspace changes via workspace-manager"
	}
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", msg)
	commitCmd.Dir = wsPath
	commitOut, err := commitCmd.CombinedOutput()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "git commit failed: " + string(commitOut)})
		return
	}

	h.emitEvent("workspace:persisted", fmt.Sprintf(`{"id":"%s","pushed":%v}`, id, req.Push))

	pushed := false
	if req.Push {
		pushCmd := exec.CommandContext(ctx, "git", "push")
		pushCmd.Dir = wsPath
		pushOut, err := pushCmd.CombinedOutput()
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{
				"committed": true,
				"pushed":    false,
				"error":     "git push failed: " + string(pushOut),
			})
			return
		}
		pushed = true
	}

	c.JSON(http.StatusOK, gin.H{
		"committed": true,
		"pushed":    pushed,
	})
}

func dirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	})
	return size
}
