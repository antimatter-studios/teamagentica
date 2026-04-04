package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
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
		ChatCommands: []pluginsdk.ChatCommand{
			{
				Name: "list", Namespace: "disk",
				Description: "List all storage disks with size and metadata",
				Endpoint:    "/chat-command/disk/list",
			},
			{
				Name: "create", Namespace: "disk",
				Description: "Create a new storage disk",
				Endpoint:    "/chat-command/disk/create",
				Params: []pluginsdk.ChatCommandParam{
					{Name: "name", Description: "Disk name (alphanumeric, hyphens, underscores, dots)", Type: "string", Required: true},
					{Name: "type", Description: "Disk type: shared or workspace (default: workspace)", Type: "string"},
				},
			},
			{
				Name: "rename", Namespace: "disk",
				Description: "Rename an existing disk",
				Endpoint:    "/chat-command/disk/rename",
				Params: []pluginsdk.ChatCommandParam{
					{Name: "disk", Description: "Current disk name", Type: "string", Required: true},
					{Name: "name", Description: "New disk name", Type: "string", Required: true},
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
	events.OnConfigUpdate(sdkClient, func(p events.ConfigUpdatePayload) {
		log.Printf("Received config:update")
	})

	// Fetch plugin config from kernel API.
	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("[storage-disk] failed to fetch plugin config: %v", err)
	}

	// Extract config values with defaults.
	dataPath := configOrDefault(pluginConfig, "STORAGE_DATA_PATH", "/data")
	storageRoot := configOrDefault(pluginConfig, "STORAGE_ROOT_PATH", "/data/storage-root")
	debug := pluginConfig["PLUGIN_DEBUG"] == "true"
	port := parseIntOrDefault(configOrDefault(pluginConfig, "STORAGE_DISK_PORT", ""), defaultPort)

	// Ensure type directories exist.
	for _, sub := range []string{"shared", "workspace"} {
		if err := os.MkdirAll(filepath.Join(storageRoot, sub), 0755); err != nil {
			log.Printf("[storage-disk] WARNING: failed to create directory %s/%s: %v", storageRoot, sub, err)
		}
	}

	router := gin.Default()
	h = handlers.NewHandler(dataPath, storageRoot, debug)

	router.GET("/health", h.Health)

	// Disk management endpoints.
	router.POST("/disks", h.CreateDisk)
	router.GET("/disks", h.ListDisks)
	router.GET("/disks/by-id/:id", h.GetDiskByID)
	router.GET("/disks/:type/:name", h.GetDisk)
	router.GET("/disks/:type/:name/path", h.GetDiskPath)
	router.PATCH("/disks/:type/:name", h.RenameDisk)
	router.DELETE("/disks/:type/:name", h.DeleteDisk)

	// File operations (scoped to within disks).
	router.GET("/browse", h.Browse)
	router.PUT("/objects/*key", h.PutObject)
	router.GET("/objects/*key", h.GetObject)
	router.DELETE("/objects/*key", h.DeleteObject)
	router.HEAD("/objects/*key", h.HeadObject)
	router.POST("/objects/copy", h.CopyObject)
	router.POST("/objects/move", h.MoveObject)
	router.GET("/download/zip", h.DownloadZip)

	// Trash endpoints (per-type, whole-disk only).
	router.GET("/trash/:type/browse", h.BrowseTrash)
	router.POST("/trash/:type/restore", h.RestoreTrash)
	router.POST("/trash/:type/empty", h.EmptyTrash)

	// Chat command handlers (used by infra-chat-command-server).
	router.POST("/chat-command/disk/list", h.ChatCommandDiskList)
	router.POST("/chat-command/disk/create", h.ChatCommandDiskCreate)
	router.POST("/chat-command/disk/rename", h.ChatCommandDiskRename)

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
	sdkClient.OnPluginAvailable("infra:mcp-server", func(p pluginsdk.PluginInfo) {
		if err := sdkClient.RegisterToolsWithMCP(p.ID, h.ToolDefs()); err != nil {
			log.Printf("failed to register tools with MCP: %v", err)
		}
	})

	sdkClient.ListenAndServe(port, router)
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
