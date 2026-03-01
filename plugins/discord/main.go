package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"roboslop/pkg/pluginsdk"
	"roboslop/plugins/discord/internal/bot"
	"roboslop/plugins/discord/internal/config"
	"roboslop/plugins/discord/internal/kernel"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Determine hostname for registration.
	hostname, _ := os.Hostname()

	// Create plugin SDK client for kernel registration + heartbeats.
	sdkCfg := pluginsdk.Config{
		KernelHost:  cfg.KernelHost,
		KernelPort:  cfg.KernelPort,
		PluginID:    cfg.PluginID,
		PluginToken: cfg.ServiceToken,
	}
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           cfg.PluginID,
		Host:         hostname,
		Port:         0, // Discord bot doesn't serve HTTP
		Capabilities: []string{"messaging:discord", "messaging:send", "messaging:receive"},
		Version:      "1.0.0",
	})

	// Start SDK (register with kernel + heartbeat loop).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sdkClient.Start(ctx)

	// Create and start the Discord bot.
	kernelClient := kernel.NewClient(cfg.KernelBaseURL(), cfg.ServiceToken)

	discordBot, err := bot.New(cfg.DiscordToken, kernelClient)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	if err := discordBot.Start(); err != nil {
		log.Fatalf("Failed to start bot: %v", err)
	}

	// Wait for shutdown signal.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	// Deregister from kernel first, then close Discord session.
	sdkClient.Stop()
	cancel()

	if err := discordBot.Stop(); err != nil {
		log.Printf("Error stopping bot: %v", err)
	}

	log.Println("Discord plugin shut down")
}
