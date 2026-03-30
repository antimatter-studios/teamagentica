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
	"github.com/antimatter-studios/teamagentica/plugins/storage-disk/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	ctx := context.Background()

	// Load SDK config from env and register with kernel.
	sdkCfg := pluginsdk.LoadConfig()

	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8090

	var h *handlers.Handler

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Name:         "Disk Storage",
		Host:         getHostname(),
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		DiscordCommands: []pluginsdk.DiscordCommand{
			{
				Name:        "disk",
				Description: "Manage storage disks",
				Subcommands: []pluginsdk.DiscordSubcommand{
					{
						Name:        "list",
						Description: "List all storage disks with size and metadata",
						Endpoint:    "/discord-command/disk/list",
					},
					{
						Name:        "create",
						Description: "Create a new storage disk",
						Endpoint:    "/discord-command/disk/create",
						Options: []pluginsdk.DiscordCommandOption{
							{Name: "name", Description: "Disk name (alphanumeric, hyphens, underscores, dots)", Type: "string", Required: true},
							{Name: "type", Description: "Disk type: auth or storage (default: storage)", Type: "string"},
						},
					},
					{
						Name:        "rename",
						Description: "Rename an existing disk",
						Endpoint:    "/discord-command/disk/rename",
						Options: []pluginsdk.DiscordCommandOption{
							{Name: "disk", Description: "Current disk name", Type: "string", Required: true},
							{Name: "name", Description: "New disk name", Type: "string", Required: true},
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
		ToolsFunc: func() interface{} {
			if h != nil {
				return h.ToolDefs()
			}
			return nil
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
		log.Fatalf("[storage-disk] failed to fetch plugin config: %v", err)
	}

	// Extract config values with defaults.
	dataPath := configOrDefault(pluginConfig, "STORAGE_DATA_PATH", "/data")
	disksPath := configOrDefault(pluginConfig, "STORAGE_DISKS_PATH", "/data/disks")
	debug := pluginConfig["PLUGIN_DEBUG"] == "true"
	port := parseIntOrDefault(configOrDefault(pluginConfig, "STORAGE_DISK_PORT", ""), defaultPort)

	// Ensure disks and metadata directories exist.
	for _, dir := range []string{disksPath, filepath.Join(dataPath, "meta")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Printf("[storage-disk] WARNING: failed to create directory %s: %v (some operations may fail)", dir, err)
		}
	}

	router := gin.Default()
	router.GET("/schema", gin.WrapF(sdkClient.SchemaHandler()))
	router.POST("/events", gin.WrapF(sdkClient.EventHandler()))
	h = handlers.NewHandler(dataPath, disksPath, debug)

	router.GET("/health", h.Health)

	// storage:api — standard file interface on dataPath.
	router.GET("/browse", h.Browse)
	router.PUT("/objects/*key", h.PutObject)
	router.GET("/objects/*key", h.GetObject)
	router.DELETE("/objects/*key", h.DeleteObject)
	router.HEAD("/objects/*key", h.HeadObject)
	router.POST("/objects/copy", h.CopyObject)
	router.POST("/objects/move", h.MoveObject)
	router.GET("/download/zip", h.DownloadZip)

	// Trash endpoints.
	router.GET("/trash/browse", h.BrowseTrash)
	router.POST("/trash/restore", h.RestoreTrash)
	router.POST("/trash/empty", h.EmptyTrash)

	// storage:disk — disk management endpoints.
	router.POST("/disks", h.CreateDisk)
	router.GET("/disks", h.ListDisks)
	router.GET("/disks/:name", h.GetDisk)
	router.GET("/disks/:name/path", h.GetDiskPath)
	router.DELETE("/disks/:name", h.DeleteDisk)

	// Discord slash command handlers.
	router.POST("/discord-command/disk/list", h.DiscordCommandDiskList)
	router.POST("/discord-command/disk/create", h.DiscordCommandDiskCreate)
	router.POST("/discord-command/disk/rename", h.DiscordCommandDiskRename)

	// Tool interface for AI agents.
	router.GET("/mcp", h.Tools)
	router.POST("/mcp/create_disk", h.ToolCreateDisk)
	router.POST("/mcp/list_disks", h.ToolListDisks)
	router.POST("/mcp/delete_disk", h.ToolDeleteDisk)
	router.POST("/mcp/list_files", h.ToolListFiles)
	router.POST("/mcp/read_file", h.ToolReadFile)
	router.POST("/mcp/write_file", h.ToolWriteFile)
	router.POST("/mcp/delete_file", h.ToolDeleteFile)
	router.POST("/mcp/browse_trash", h.ToolBrowseTrash)
	router.POST("/mcp/restore_from_trash", h.ToolRestoreFromTrash)
	router.POST("/mcp/empty_trash", h.ToolEmptyTrash)

	// Push tools to MCP server when it becomes available.
	sdkClient.WhenPluginAvailable("infra:mcp-server", func(p pluginsdk.PluginInfo) {
		if err := sdkClient.RegisterToolsWithMCP(p.ID, h.ToolDefs()); err != nil {
			log.Printf("failed to register tools with MCP: %v", err)
		}
	})

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
