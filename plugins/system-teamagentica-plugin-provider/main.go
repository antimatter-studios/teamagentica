package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/system-teamagentica-plugin-provider/internal/store"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	const defaultPort = 8083

	hostname, _ := os.Hostname()

	sdkCfg := pluginsdk.LoadConfig()
	manifest := pluginsdk.LoadManifest()
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
	})

	sdkClient.Start(context.Background())

	// Open the catalog database.
	dbPath := "/data/catalog.db"
	catalog, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("failed to open catalog db: %v", err)
	}
	log.Printf("catalog: opened %s (%d plugins)", dbPath, catalog.Count())

	// Seed the default system provider (self).
	selfURL := fmt.Sprintf("https://teamagentica-plugin-%s:%d", manifest.ID, defaultPort)
	if err := catalog.SeedProvider("TeamAgentica Plugin Provider", selfURL, true); err != nil {
		log.Printf("catalog: failed to seed default provider: %v", err)
	} else {
		log.Printf("catalog: default provider seeded (url=%s)", selfURL)
	}

	r := gin.Default()
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// --- Provider management ---

	r.GET("/providers", func(c *gin.Context) {
		providers, err := catalog.ListProviders()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch providers"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"providers": providers})
	})

	r.POST("/providers", func(c *gin.Context) {
		var req struct {
			Name string `json:"name" binding:"required"`
			URL  string `json:"url" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		p, err := catalog.CreateProvider(req.Name, req.URL)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add provider: " + err.Error()})
			return
		}
		c.JSON(http.StatusCreated, gin.H{"provider": p})
	})

	r.DELETE("/providers/:id", func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid provider id"})
			return
		}
		if err := catalog.DeleteProvider(uint(id)); err != nil {
			status := http.StatusInternalServerError
			if err.Error() == "provider not found" {
				status = http.StatusNotFound
			} else if err.Error() == "system providers cannot be deleted" {
				status = http.StatusForbidden
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "provider deleted"})
	})

	// Browse catalog — returns entries for marketplace UI.
	r.GET("/plugins", func(c *gin.Context) {
		q := c.Query("q")
		results := catalog.Search(q)
		if results == nil {
			results = []store.Entry{}
		}
		c.JSON(http.StatusOK, gin.H{
			"plugins": results,
			"groups":  store.Groups,
		})
	})

	// Full manifest for install — returns everything from plugin.yaml.
	r.GET("/plugins/:id/manifest", func(c *gin.Context) {
		id := c.Param("id")
		manifest, ok := catalog.GetManifest(id)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found: " + id})
			return
		}
		c.JSON(http.StatusOK, manifest)
	})

	// Submit a plugin manifest — upserts by plugin_id + version.
	r.POST("/manifests", func(c *gin.Context) {
		var data map[string]interface{}
		if err := c.ShouldBindJSON(&data); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		pluginID, _ := data["id"].(string)
		if pluginID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "manifest must have an 'id' field"})
			return
		}

		version, _ := data["version"].(string)
		if version == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "manifest must have a 'version' field"})
			return
		}

		if err := catalog.Upsert(pluginID, version, data); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store manifest: " + err.Error()})
			return
		}

		log.Printf("catalog: upserted %s@%s", pluginID, version)
		c.JSON(http.StatusOK, gin.H{"message": "manifest stored", "plugin_id": pluginID, "version": version})
	})

	// Remove all versions of a plugin from the catalog.
	r.DELETE("/plugins/:id", func(c *gin.Context) {
		id := c.Param("id")
		affected, err := catalog.Delete(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete: " + err.Error()})
			return
		}
		if affected == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "plugin not found: " + id})
			return
		}
		log.Printf("catalog: deleted %s (%d versions)", id, affected)
		c.JSON(http.StatusOK, gin.H{"message": "plugin removed from catalog", "plugin_id": id, "versions_removed": affected})
	})

	sdkClient.ListenAndServe(defaultPort, r)
}
