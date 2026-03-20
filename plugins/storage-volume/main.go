package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/storage-volume/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	ctx := context.Background()

	// Load SDK config from env and register with kernel.
	sdkCfg := pluginsdk.LoadConfig()

	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8090

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Name:         "Volume Storage",
		Host:         getHostname(),
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		DiscordCommands: []pluginsdk.DiscordCommand{
			{
				Name:        "volume",
				Description: "Manage storage volumes",
				Subcommands: []pluginsdk.DiscordSubcommand{
					{
						Name:        "list",
						Description: "List all storage volumes with size and metadata",
						Endpoint:    "/discord-command/volume/list",
					},
					{
						Name:        "create",
						Description: "Create a new storage volume",
						Endpoint:    "/discord-command/volume/create",
						Options: []pluginsdk.DiscordCommandOption{
							{Name: "name", Description: "Volume name (alphanumeric, hyphens, underscores, dots)", Type: "string", Required: true},
							{Name: "type", Description: "Volume type: auth or storage (default: storage)", Type: "string"},
						},
					},
					{
						Name:        "rename",
						Description: "Rename an existing volume",
						Endpoint:    "/discord-command/volume/rename",
						Options: []pluginsdk.DiscordCommandOption{
							{Name: "volume", Description: "Current volume name", Type: "string", Required: true},
							{Name: "name", Description: "New volume name", Type: "string", Required: true},
						},
					},
				},
			},
		},
		SchemaFunc: func() map[string]interface{} {
			return map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
		},
	})
	sdkClient.Start(ctx)

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
			log.Printf("[storage-volume] WARNING: failed to create directory %s: %v (some operations may fail)", dir, err)
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

	// Discord slash command handlers.
	router.POST("/discord-command/volume/list", h.DiscordCommandVolumeList)
	router.POST("/discord-command/volume/create", h.DiscordCommandVolumeCreate)
	router.POST("/discord-command/volume/rename", h.DiscordCommandVolumeRename)

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
