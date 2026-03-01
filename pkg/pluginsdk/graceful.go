package pluginsdk

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// RunWithGracefulShutdown starts an HTTP server and handles SIGINT/SIGTERM.
// On signal: calls sdkClient.Stop() to deregister, then shuts down the HTTP
// server with a 10-second timeout.
func RunWithGracefulShutdown(server *http.Server, sdkClient *Client) {
	// Start server in a goroutine.
	go func() {
		log.Printf("pluginsdk: server listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("pluginsdk: server error: %v", err)
		}
	}()

	// Wait for interrupt signal.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Printf("pluginsdk: received signal %s, shutting down", sig)

	// Deregister from the kernel first.
	sdkClient.Stop()

	// Gracefully shut down the HTTP server.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("pluginsdk: server shutdown error: %v", err)
	}

	log.Println("pluginsdk: shutdown complete")
}
