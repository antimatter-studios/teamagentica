package pluginsdk

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// ListenAndServe starts an HTTP server on the given port with the SDK's
// standard routes (/schema, /events) pre-wired, mTLS configured automatically,
// and graceful shutdown on SIGINT/SIGTERM.
//
// The handler can be any http.Handler — gin.Engine, chi.Mux, http.ServeMux, etc.
// The SDK intercepts /schema and /events before they reach the handler;
// all other requests pass through to the handler unchanged.
//
// This is a blocking call. It returns only after shutdown completes.
//
// Usage:
//
//	router := gin.Default()
//	router.POST("/chat", handleChat)
//	sdkClient.ListenAndServe(port, router)
func (c *Client) ListenAndServe(port int, handler http.Handler) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /schema", c.SchemaHandler())
	mux.HandleFunc("POST /events", c.EventHandler())
	mux.Handle("/", handler)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	tlsConfig, err := GetServerTLSConfig(c.config)
	if err != nil {
		log.Fatalf("pluginsdk: failed to configure server TLS: %v", err)
	}

	go func() {
		if tlsConfig != nil {
			server.TLSConfig = tlsConfig
			log.Printf("pluginsdk: server listening on %s (mTLS enabled)", server.Addr)
			if err := server.ListenAndServeTLS(c.config.TLSCert, c.config.TLSKey); err != nil && err != http.ErrServerClosed {
				log.Fatalf("pluginsdk: server error: %v", err)
			}
		} else {
			log.Printf("pluginsdk: server listening on %s", server.Addr)
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("pluginsdk: server error: %v", err)
			}
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Printf("pluginsdk: received signal %s, shutting down", sig)

	c.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("pluginsdk: server shutdown error: %v", err)
	}

	log.Println("pluginsdk: shutdown complete")
}
