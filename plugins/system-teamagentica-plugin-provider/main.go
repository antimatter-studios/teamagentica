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

	// Open the catalog database.
	dbPath := os.Getenv("CATALOG_DB")
	if dbPath == "" {
		dbPath = "/data/catalog.db"
	}
	catalog, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("failed to open catalog db: %v", err)
	}
	log.Printf("catalog: opened %s (%d plugins)", dbPath, catalog.Count())

	port := os.Getenv("PROVIDER_PORT")
	if port == "" {
		port = "8083"
	}
	portInt := 8083
	fmt.Sscanf(port, "%d", &portInt)

	hostname, _ := os.Hostname()

	sdkCfg := pluginsdk.LoadConfig()
	manifest := pluginsdk.LoadManifest()
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         hostname,
		Port:         portInt,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
	})

	sdkClient.Start(context.Background())

	r := gin.Default()

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

	server := &http.Server{
		Addr:    "0.0.0.0:" + port,
		Handler: r,
	}

	log.Printf("system-teamagentica-plugin-provider starting on %s", server.Addr)
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
	log.Println("system-teamagentica-plugin-provider shut down")
}
