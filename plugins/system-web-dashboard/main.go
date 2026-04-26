package main

import (
	"context"
	"log"
	"os"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/system-web-dashboard/internal/server"
)

const defaultAPIPort = 8080

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()
	manifest := pluginsdk.LoadManifest()
	hostname, _ := os.Hostname()

	apiPort := defaultAPIPort

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Name:         manifest.Name,
		Host:         hostname,
		Port:         apiPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		Dependencies: pluginsdk.PluginDependencies{
			Capabilities: []string{"infra:authz"},
		},
		SchemaFunc: func() map[string]interface{} {
			return map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
		},
	})

	sdkClient.Start(context.Background())

	// API server (mTLS) is started via SDK helper. Blocks until shutdown.
	apiHandler := server.NewAPIHandler()
	sdkClient.ListenAndServe(apiPort, apiHandler)
}
