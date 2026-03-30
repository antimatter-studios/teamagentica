package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

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

	r := gin.Default()
	r.GET("/schema", gin.WrapF(sdkClient.SchemaHandler()))
	r.POST("/events", gin.WrapF(sdkClient.EventHandler()))

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
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

	server := &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%d", defaultPort),
		Handler: r,
	}

	log.Printf("system-teamagentica-plugin-provider starting on %s", server.Addr)
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
	log.Println("system-teamagentica-plugin-provider shut down")
}
