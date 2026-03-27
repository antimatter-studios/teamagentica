package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/agent-ollama/internal/handlers"
	"github.com/antimatter-studios/teamagentica/plugins/agent-ollama/internal/ollama"
)

//go:embed system-prompt.md
var defaultSystemPrompt string

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sdkCfg := pluginsdk.LoadConfig()
	manifest := pluginsdk.LoadManifest()

	const defaultPort = 8083

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         getHostname(),
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		Dependencies: pluginsdk.PluginDependencies{Capabilities: manifest.Dependencies},
		SchemaFunc: func() map[string]interface{} {
			return map[string]interface{}{
				"config": manifest.ConfigSchema,
			}
		},
	})

	ctx := context.Background()
	sdkClient.Start(ctx)

	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("Failed to fetch plugin config: %v", err)
	}

	model := configOrDefault(pluginConfig, "OLLAMA_MODEL", "llama3.2:3b")
	ollamaEndpoint := "http://localhost:11434" // local Ollama managed by supervisord
	dataPath := "/data"
	debug := pluginConfig["PLUGIN_DEBUG"] == "true"

	toolLoopLimit := 20
	if v := pluginConfig["TOOL_LOOP_LIMIT"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			toolLoopLimit = n
		}
	}

	// Pull configured models in background.
	modelsToPull := parseModelList(pluginConfig["OLLAMA_MODELS"], []string{"llama3.2:3b", "nomic-embed-text"})
	go pullModels(ollamaEndpoint, modelsToPull)

	router := gin.Default()

	h := handlers.NewHandler(handlers.HandlerConfig{
		Model:               model,
		Endpoint:            ollamaEndpoint,
		ToolLoopLimit:       toolLoopLimit,
		Debug:               debug,
		DataPath:            dataPath,
		DefaultSystemPrompt: defaultSystemPrompt,
	})

	h.SetModelList(modelsToPull)

	// Routes.
	router.GET("/health", h.Health)
	router.POST("/chat", h.Chat)
	router.POST("/chat/stream", h.ChatStream)
	router.GET("/mcp", h.DiscoveredTools)
	router.GET("/system-prompt", h.SystemPrompt)
	router.GET("/models", h.Models)
	router.POST("/models/pull", h.PullModel)
	router.POST("/models/delete", h.DeleteModel)
	router.GET("/config/options/:field", h.ConfigOptions)
	router.GET("/usage", h.Usage)
	router.GET("/usage/records", h.UsageRecords)

	// Config updates in-place.
	sdkClient.OnEvent("config:update", pluginsdk.NewNullDebouncer(func(event pluginsdk.EventCallback) {
		var detail struct {
			Config map[string]string `json:"config"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &detail); err != nil {
			log.Printf("[config] failed to parse config:update: %v", err)
			return
		}
		h.ApplyConfig(detail.Config)
	}))

	// Pricing (Ollama is free, but track for consistency).
	pricing := pluginsdk.NewPricingHandler([]pluginsdk.PricingEntry{
		{Provider: "ollama", Model: "llama3.2:3b", InputPer1M: 0, OutputPer1M: 0, Currency: "USD"},
	}, sdkClient)
	router.GET("/pricing", gin.WrapF(pricing.HandleGet))
	router.PUT("/pricing", gin.WrapF(pricing.HandlePut))

	// OpenAI-compatible proxy — forwards to internal Ollama's OpenAI-compat layer.
	// Allows other plugins (e.g. infra-agent-memory) to use Ollama models via standard endpoints.
	router.Any("/v1/*path", h.OpenAIProxy)

	h.SetSDK(sdkClient)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", defaultPort),
		Handler: router,
	}
	pluginsdk.RunWithGracefulShutdown(server, sdkClient)
}

// parseModelList parses a JSON array of model names, falling back to defaults.
func parseModelList(raw string, defaults []string) []string {
	if raw == "" {
		return defaults
	}
	var models []string
	if err := json.Unmarshal([]byte(raw), &models); err != nil {
		// Fallback: try comma-separated for backwards compat.
		for _, m := range strings.Split(raw, ",") {
			if t := strings.TrimSpace(m); t != "" {
				models = append(models, t)
			}
		}
	}
	if len(models) == 0 {
		return defaults
	}
	return models
}

// pullModels waits for Ollama to be ready and pulls all configured models.
func pullModels(endpoint string, models []string) {

	// Wait for Ollama to be ready.
	for i := 0; i < 60; i++ {
		if err := ollama.Healthy(endpoint); err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}

	for _, m := range models {
		if m == "" {
			continue
		}
		log.Printf("[ollama] pulling model %s...", m)
		if err := ollama.PullModel(endpoint, m); err != nil {
			log.Printf("[ollama] failed to pull %s: %v", m, err)
		} else {
			log.Printf("[ollama] model %s ready", m)
		}
	}
}

func configOrDefault(m map[string]string, key, fallback string) string {
	if v, ok := m[key]; ok && v != "" {
		return v
	}
	return fallback
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		log.Printf("Failed to get hostname: %v", err)
		return "localhost"
	}
	return hostname
}
