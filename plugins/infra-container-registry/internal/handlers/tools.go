package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ToolDefs returns MCP tool definitions for the registry.
func (h *Handler) ToolDefs() []gin.H {
	return []gin.H{
		{
			"name":        "list_images",
			"description": "List all images in the self-hosted Docker registry",
			"endpoint":    "/mcp/list_images",
			"parameters": gin.H{
				"type":       "object",
				"properties": gin.H{},
			},
		},
		{
			"name":        "list_tags",
			"description": "List all tags for a specific image in the registry",
			"endpoint":    "/mcp/list_tags",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"image": gin.H{"type": "string", "description": "Image name (e.g. 'teamagentica-agent-claude')"},
				},
				"required": []string{"image"},
			},
		},
		{
			"name":        "delete_image",
			"description": "Delete an image tag from the registry",
			"endpoint":    "/mcp/delete_image",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"image": gin.H{"type": "string", "description": "Image name"},
					"tag":   gin.H{"type": "string", "description": "Tag to delete"},
				},
				"required": []string{"image", "tag"},
			},
		},
	}
}

// Tools returns the tool definitions.
func (h *Handler) Tools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": h.ToolDefs()})
}

// ToolListImages handles POST /mcp/list_images.
func (h *Handler) ToolListImages(c *gin.Context) {
	repos, err := h.fetchCatalog()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	results := make([]gin.H, 0, len(repos))
	for _, repo := range repos {
		tags, _ := h.fetchTags(repo)
		results = append(results, gin.H{
			"image":     repo,
			"tag_count": len(tags),
			"latest":    latestTag(tags),
		})
	}

	c.JSON(http.StatusOK, gin.H{"images": results})
}

// ToolListTags handles POST /mcp/list_tags.
func (h *Handler) ToolListTags(c *gin.Context) {
	var req struct {
		Image string `json:"image" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tags, err := h.fetchTags(req.Image)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"image": req.Image, "tags": tags})
}

// ToolDeleteImage handles POST /mcp/delete_image.
func (h *Handler) ToolDeleteImage(c *gin.Context) {
	var req struct {
		Image string `json:"image" binding:"required"`
		Tag   string `json:"tag" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	digest, err := h.resolveDigest(req.Image, req.Tag)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	delReq, _ := http.NewRequest("DELETE", registryURL+"/v2/"+req.Image+"/manifests/"+digest, nil)
	resp, err := h.client.Do(delReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		c.JSON(resp.StatusCode, gin.H{"error": "delete failed", "status": resp.StatusCode})
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": req.Image + ":" + req.Tag})
}
