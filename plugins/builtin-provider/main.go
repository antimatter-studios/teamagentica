package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/builtin-provider/internal/store"
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

	// Seed from baked-in manifests on first boot (idempotent).
	manifestsDir := os.Getenv("MANIFESTS_DIR")
	if manifestsDir == "" {
		manifestsDir = "/usr/local/etc/teamagentica/manifests"
	}
	seedCatalogFromDir(catalog, manifestsDir)

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

	log.Printf("builtin-provider starting on %s", server.Addr)
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
	log.Println("builtin-provider shut down")
}

// seedCatalogFromDir reads all *.yaml files in dir and upserts them into the catalog.
// Safe to call on every startup — Upsert is idempotent per (plugin_id, version).
func seedCatalogFromDir(catalog *store.Store, dir string) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil || len(matches) == 0 {
		return
	}
	var seeded, skipped int
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("catalog seed: skip %s: %v", path, err)
			skipped++
			continue
		}
		var manifest map[string]interface{}
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			log.Printf("catalog seed: skip %s: bad yaml: %v", path, err)
			skipped++
			continue
		}
		id, _ := manifest["id"].(string)
		version, _ := manifest["version"].(string)
		if id == "" || version == "" {
			skipped++
			continue
		}
		// store.Upsert expects JSON-serialisable map; yaml.Unmarshal produces
		// map[string]interface{} which json.Marshal handles correctly.
		jsonSafe := toJSONSafe(manifest)
		if err := catalog.Upsert(id, version, jsonSafe); err != nil {
			log.Printf("catalog seed: failed %s: %v", id, err)
			skipped++
			continue
		}
		seeded++
	}
	if seeded+skipped > 0 {
		log.Printf("catalog seed: %d seeded, %d skipped from %s", seeded, skipped, dir)
	}
}

// toJSONSafe round-trips through JSON to normalise any types that yaml.Unmarshal
// produces (e.g. map[interface{}]interface{}) into JSON-compatible equivalents.
func toJSONSafe(in map[string]interface{}) map[string]interface{} {
	b, err := json.Marshal(in)
	if err != nil {
		return in
	}
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return in
	}
	return out
}
