package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/storage-sss3/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/storage-sss3/internal/index"
	"github.com/antimatter-studios/teamagentica/plugins/storage-sss3/internal/s3client"
	"github.com/antimatter-studios/teamagentica/plugins/storage-sss3/internal/sss3proc"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load SDK config from env and register with kernel.
	sdkCfg := pluginsdk.LoadConfig()

	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8081

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Name:         "Object Storage",
		Host:         getHostname(),
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
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
		log.Fatalf("[storage-sss3] failed to fetch plugin config: %v", err)
	}

	// Extract config values with defaults.
	s3Bucket := configOrDefault(pluginConfig, "S3_BUCKET", "teamagentica")
	s3AccessKey := configOrDefault(pluginConfig, "S3_ACCESS_KEY", "minioadmin")
	s3SecretKey := configOrDefault(pluginConfig, "S3_SECRET_KEY", "minioadmin")
	s3Region := configOrDefault(pluginConfig, "S3_REGION", "us-east-1")
	storagePath := configOrDefault(pluginConfig, "SSS3_STORAGE_PATH", "/data/sss3")
	debug := pluginConfig["PLUGIN_DEBUG"] == "true"

	port := parseIntOrDefault(configOrDefault(pluginConfig, "SSS3_STORAGE_PORT", ""), defaultPort)
	s3Port := parseIntOrDefault(configOrDefault(pluginConfig, "SSS3_PORT", ""), 5553)

	// Start sss3 subprocess.
	var client *s3client.Client
	var idx *index.Index

	if err := sss3proc.Start(ctx, sss3proc.Config{
		Port:        s3Port,
		StoragePath: storagePath,
		AccessKey:   s3AccessKey,
		SecretKey:   s3SecretKey,
		Bucket:      s3Bucket,
	}); err != nil {
		log.Printf("[storage-sss3] WARNING: failed to start sss3 (running degraded): %v", err)
	} else {
		// Initialize S3 client only if sss3 started successfully.
		client = s3client.New(s3client.S3Config{
			Port:      s3Port,
			Region:    s3Region,
			AccessKey: s3AccessKey,
			SecretKey: s3SecretKey,
			Bucket:    s3Bucket,
			Debug:     debug,
		})
		if err := client.EnsureBucket(ctx); err != nil {
			log.Printf("[storage-sss3] WARNING: bucket setup failed (will retry on first use): %v", err)
		}

		// Initialize and warm the metadata index.
		idx = index.New(client)
		if err := idx.Warm(ctx); err != nil {
			log.Printf("[storage-sss3] WARNING: index warm failed (will retry on refresh): %v", err)
		}
	}

	// Set up routes.
	router := gin.Default()
	h := handlers.NewHandler(client, idx)

	router.GET("/health", h.Health)
	router.PUT("/objects/*key", h.PutObject)
	router.GET("/objects/*key", h.GetObject)
	router.DELETE("/objects/*key", h.DeleteObject)
	router.HEAD("/objects/*key", h.HeadObject)
	router.GET("/browse", h.Browse)
	router.GET("/list", h.List)
	router.POST("/refresh", h.Refresh)

	// Tool interface — allows AI agents to discover and use storage operations.
	router.GET("/tools", h.Tools)
	router.POST("/tool/list_files", h.ToolListFiles)
	router.POST("/tool/read_file", h.ToolReadFile)
	router.POST("/tool/write_file", h.ToolWriteFile)
	router.POST("/tool/delete_file", h.ToolDeleteFile)
	router.POST("/tool/file_info", h.ToolFileInfo)
	router.POST("/tool/create_folder", h.ToolCreateFolder)

	// Proxy unmatched routes to sss3 S3 API.
	sss3URL, _ := url.Parse(fmt.Sprintf("http://localhost:%d", s3Port))
	router.NoRoute(gin.WrapH(httputil.NewSingleHostReverseProxy(sss3URL)))

	h.SetSDK(sdkClient)

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
