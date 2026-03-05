package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/builtin-provider/internal/catalog"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	port := os.Getenv("PROVIDER_PORT")
	if port == "" {
		port = "8083"
	}
	portInt := 8083
	fmt.Sscanf(port, "%d", &portInt)

	hostname, _ := os.Hostname()

	sdkCfg := pluginsdk.LoadConfig()
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           sdkCfg.PluginID,
		Host:         hostname,
		Port:         portInt,
		Capabilities: []string{"marketplace:provider"},
		Version:      "1.0.0",
	})

	sdkClient.Start(context.Background())

	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.GET("/plugins", func(c *gin.Context) {
		q := c.Query("q")
		results := catalog.Search(q)
		c.JSON(http.StatusOK, gin.H{"plugins": results})
	})

	server := &http.Server{
		Addr:    "0.0.0.0:" + port,
		Handler: r,
	}

	log.Printf("builtin-provider starting on %s", server.Addr)
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
	log.Println("builtin-provider shut down")
}
