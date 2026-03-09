package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/storage-volume/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	ctx := context.Background()

	// Load SDK config from env and register with kernel.
	sdkCfg := pluginsdk.LoadConfig()

	pluginID := os.Getenv("TEAMAGENTICA_PLUGIN_ID")
	if pluginID == "" {
		pluginID = "storage-volume"
	}

	const defaultPort = 8090

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           pluginID,
		Name:         "Volume Storage",
		Host:         getHostname(),
		Port:         defaultPort,
		Capabilities: []string{"storage:block", "storage:api"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"STORAGE_DATA_PATH":    {Type: "string", Label: "Data Path", Default: "/data", HelpText: "Local filesystem path for volume storage", Order: 1},
			"STORAGE_VOLUMES_PATH": {Type: "string", Label: "Volumes Path", Default: "/data/volumes", HelpText: "Path for namespace-isolated block storage volumes", Order: 2},
			"STORAGE_VOLUME_PORT":  {Type: "string", Label: "Plugin Port", Default: "8090", HelpText: "HTTP port for the storage plugin", Order: 3},
			"PLUGIN_ALIASES":     {Type: "aliases", Label: "Aliases", HelpText: "Define routing aliases for this plugin.", Order: 90},
			"PLUGIN_DEBUG":       {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed operations", Order: 99},
		},
	})
	// Seed aliases from kernel (will update dynamically via alias:update events).
	entries, err := sdkClient.FetchAliases()
	if err != nil {
		log.Printf("Kernel alias fetch failed: %v (aliases will update via events)", err)
	}
	if len(entries) > 0 {
		log.Printf("Loaded %d aliases from kernel", len(entries))
	}
	aliases := alias.NewAliasMap(entries)

	sdkClient.Start(ctx)

	// Subscribe to live alias updates from kernel (debounced).
	sdkClient.OnEvent("kernel:alias:update", pluginsdk.NewTimedDebouncer(2*time.Second, func(event pluginsdk.EventCallback) {
		var detail struct {
			Aliases []alias.AliasInfo `json:"aliases"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("Failed to parse kernel:alias:update detail: %v", err)
			return
		}
		aliases.Replace(detail.Aliases)
		log.Printf("Hot-swapped %d aliases (seq=%d)", len(detail.Aliases), event.Seq)
	}))

	// Subscribe to config updates.
	sdkClient.OnEvent("config:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		log.Printf("Received config:update (seq=%d)", event.Seq)
	}))

	// Fetch plugin config from kernel API.
	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("[storage-volume] failed to fetch plugin config: %v", err)
	}

	// Extract config values with defaults.
	dataPath := configOrDefault(pluginConfig, "STORAGE_DATA_PATH", "/data")
	volumesPath := configOrDefault(pluginConfig, "STORAGE_VOLUMES_PATH", "/data/volumes")
	debug := pluginConfig["PLUGIN_DEBUG"] == "true"
	port := parseIntOrDefault(configOrDefault(pluginConfig, "STORAGE_VOLUME_PORT", ""), defaultPort)

	// Ensure volumes and metadata directories exist.
	for _, dir := range []string{volumesPath, filepath.Join(dataPath, "meta")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Fatalf("[storage-volume] failed to create directory %s: %v", dir, err)
		}
	}

	router := gin.Default()
	h := handlers.NewHandler(dataPath, volumesPath, debug)

	router.GET("/health", h.Health)

	// storage:api — standard file interface on dataPath.
	router.GET("/browse", h.Browse)
	router.PUT("/objects/*key", h.PutObject)
	router.GET("/objects/*key", h.GetObject)
	router.DELETE("/objects/*key", h.DeleteObject)
	router.HEAD("/objects/*key", h.HeadObject)

	// storage:block — volume management endpoints.
	router.POST("/volumes", h.CreateVolume)
	router.GET("/volumes", h.ListVolumes)
	router.GET("/volumes/:name", h.GetVolume)
	router.GET("/volumes/:name/path", h.GetVolumePath)
	router.DELETE("/volumes/:name", h.DeleteVolume)

	// Tool interface for AI agents.
	router.GET("/tools", h.Tools)
	router.POST("/tool/create_volume", h.ToolCreateVolume)
	router.POST("/tool/list_volumes", h.ToolListVolumes)
	router.POST("/tool/delete_volume", h.ToolDeleteVolume)
	router.POST("/tool/list_files", h.ToolListFiles)
	router.POST("/tool/read_file", h.ToolReadFile)
	router.POST("/tool/write_file", h.ToolWriteFile)
	router.POST("/tool/delete_file", h.ToolDeleteFile)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}

func configOrDefault(m map[string]string, key, fallback string) string {
	if v, ok := m[key]; ok && v != "" {
		return v
	}
	return fallback
}

func parseIntOrDefault(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return v
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		log.Printf("Failed to get hostname: %v", err)
		return "localhost"
	}
	return hostname
}
