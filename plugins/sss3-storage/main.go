package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/sss3-storage/internal/config"
	"github.com/antimatter-studios/teamagentica/plugins/sss3-storage/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/sss3-storage/internal/index"
	"github.com/antimatter-studios/teamagentica/plugins/sss3-storage/internal/s3client"
	"github.com/antimatter-studios/teamagentica/plugins/sss3-storage/internal/sss3proc"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg := config.Load()

	// Start sss3 subprocess
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := sss3proc.Start(ctx, sss3proc.Config{
		Port:        cfg.S3Port,
		StoragePath: cfg.StoragePath,
		AccessKey:   cfg.S3AccessKey,
		SecretKey:   cfg.S3SecretKey,
		Bucket:      cfg.S3Bucket,
	}); err != nil {
		log.Fatalf("[sss3-storage] failed to start sss3: %v", err)
	}

	// Initialize S3 client
	client := s3client.New(cfg)
	if err := client.EnsureBucket(ctx); err != nil {
		log.Printf("[sss3-storage] WARNING: bucket setup failed (will retry on first use): %v", err)
	}

	// Initialize and warm the metadata index
	idx := index.New(client)
	if err := idx.Warm(ctx); err != nil {
		log.Printf("[sss3-storage] WARNING: index warm failed (will retry on refresh): %v", err)
	}

	// Set up routes
	router := gin.Default()
	h := handlers.NewHandler(cfg, client, idx)

	router.GET("/health", h.Health)
	router.PUT("/objects/*key", h.PutObject)
	router.GET("/objects/*key", h.GetObject)
	router.DELETE("/objects/*key", h.DeleteObject)
	router.HEAD("/objects/*key", h.HeadObject)
	router.GET("/browse", h.Browse)
	router.GET("/list", h.List)
	router.POST("/refresh", h.Refresh)

	// Proxy unmatched routes to sss3 S3 API
	sss3URL, _ := url.Parse(fmt.Sprintf("http://localhost:%d", cfg.S3Port))
	router.NoRoute(gin.WrapH(httputil.NewSingleHostReverseProxy(sss3URL)))

	// Register with kernel
	sdkCfg := pluginsdk.LoadConfig()
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           cfg.PluginID,
		Host:         getHostname(),
		Port:         cfg.Port,
		Capabilities: []string{"storage:api"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"S3_ENDPOINT":   {Type: "string", Label: "S3 Endpoint", Required: true, Default: "http://sss3:9000", HelpText: "S3-compatible endpoint URL", Order: 1},
			"S3_BUCKET":     {Type: "string", Label: "Bucket Name", Required: true, Default: "teamagentica", HelpText: "S3 bucket name for storage", Order: 2},
			"S3_ACCESS_KEY": {Type: "string", Label: "Access Key", Required: true, Secret: true, Default: "minioadmin", HelpText: "S3 access key", Order: 3},
			"S3_SECRET_KEY": {Type: "string", Label: "Secret Key", Required: true, Secret: true, Default: "minioadmin", HelpText: "S3 secret key", Order: 4},
			"S3_REGION":     {Type: "string", Label: "Region", Default: "us-east-1", HelpText: "S3 region", Order: 5},
			"PLUGIN_DEBUG":  {Type: "boolean", Label: "Debug Mode", Default: "false", HelpText: "Log detailed S3 operations", Order: 99},
		},
	})
	sdkClient.Start(ctx)
	h.SetSDK(sdkClient)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		log.Printf("Failed to get hostname: %v", err)
		return "localhost"
	}
	return hostname
}
