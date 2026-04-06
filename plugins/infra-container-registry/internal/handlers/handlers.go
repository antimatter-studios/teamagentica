package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

const registryURL = "http://localhost:5000"

// Handler wraps the Docker Registry v2 HTTP API.
type Handler struct {
	sdk    *pluginsdk.Client
	debug  bool
	client *http.Client
}

func NewHandler(sdk *pluginsdk.Client, debug bool) *Handler {
	return &Handler{
		sdk:    sdk,
		debug:  debug,
		client: &http.Client{},
	}
}

// Health checks that the registry is responding.
func (h *Handler) Health(c *gin.Context) {
	resp, err := h.client.Get(registryURL + "/v2/")
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "error": err.Error()})
		return
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "error": fmt.Sprintf("registry returned %d", resp.StatusCode)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "healthy"})
}

// ListImages returns all repositories in the registry.
func (h *Handler) ListImages(c *gin.Context) {
	repos, err := h.fetchCatalog()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"images": repos})
}

// ListTags returns tags for a given image.
func (h *Handler) ListTags(c *gin.Context) {
	name := c.Param("name")
	tags, err := h.fetchTags(name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"name": name, "tags": tags})
}

// DeleteImage deletes an image by name and tag.
func (h *Handler) DeleteImage(c *gin.Context) {
	name := c.Param("name")
	tag := c.Param("tag")

	// Resolve tag to digest.
	digest, err := h.resolveDigest(name, tag)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Delete by digest.
	url := fmt.Sprintf("%s/v2/%s/manifests/%s", registryURL, name, digest)
	req, _ := http.NewRequest("DELETE", url, nil)
	resp, err := h.client.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		c.JSON(resp.StatusCode, gin.H{"error": fmt.Sprintf("delete returned %d", resp.StatusCode)})
		return
	}

	log.Printf("deleted %s:%s (digest %s)", name, tag, digest)
	c.JSON(http.StatusOK, gin.H{"deleted": name + ":" + tag})
}

// RegistryStats returns summary info for the schema endpoint.
func (h *Handler) RegistryStats() (map[string]interface{}, error) {
	repos, err := h.fetchCatalog()
	if err != nil {
		return nil, err
	}

	items := make([]map[string]interface{}, 0, len(repos))
	for _, repo := range repos {
		tags, err := h.fetchTags(repo)
		if err != nil {
			continue
		}
		items = append(items, map[string]interface{}{
			"image":    repo,
			"tags":     len(tags),
			"latest":   latestTag(tags),
		})
	}

	return map[string]interface{}{
		"total_images": len(repos),
		"images": map[string]interface{}{
			"_display": "table",
			"_columns": []string{"image", "tags", "latest"},
			"items":    items,
		},
	}, nil
}

// fetchCatalog returns the list of repositories from the registry.
func (h *Handler) fetchCatalog() ([]string, error) {
	resp, err := h.client.Get(registryURL + "/v2/_catalog")
	if err != nil {
		return nil, fmt.Errorf("registry unreachable: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid catalog response: %w", err)
	}
	return result.Repositories, nil
}

// fetchTags returns tags for a repository.
func (h *Handler) fetchTags(name string) ([]string, error) {
	resp, err := h.client.Get(fmt.Sprintf("%s/v2/%s/tags/list", registryURL, name))
	if err != nil {
		return nil, fmt.Errorf("registry unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("image not found: %s", name)
	}

	var result struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid tags response: %w", err)
	}
	return result.Tags, nil
}

// resolveDigest gets the digest for a tag (needed for deletion).
func (h *Handler) resolveDigest(name, tag string) (string, error) {
	url := fmt.Sprintf("%s/v2/%s/manifests/%s", registryURL, name, tag)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")

	resp, err := h.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to resolve digest: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("manifest not found: %s:%s (status %d)", name, tag, resp.StatusCode)
	}

	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", fmt.Errorf("no digest header for %s:%s", name, tag)
	}
	return digest, nil
}

func latestTag(tags []string) string {
	if len(tags) == 0 {
		return "-"
	}
	for _, t := range tags {
		if t == "latest" {
			return "latest"
		}
	}
	return tags[len(tags)-1]
}
