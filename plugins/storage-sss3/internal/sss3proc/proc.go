package sss3proc

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// Config maps plugin config to sss3 environment variables.
type Config struct {
	Port        int
	StoragePath string
	AccessKey   string
	SecretKey   string
	Bucket      string
}

// Start launches the sss3 binary as a child process.
// It blocks until sss3 is healthy or the context is cancelled.
func Start(ctx context.Context, cfg Config) error {
	cmd := exec.CommandContext(ctx, "stupid-simple-s3")
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("STUPID_PORT=%d", cfg.Port),
		fmt.Sprintf("STUPID_STORAGE_PATH=%s", cfg.StoragePath),
		fmt.Sprintf("STUPID_MULTIPART_PATH=%s-tmp", cfg.StoragePath),
		fmt.Sprintf("STUPID_RW_ACCESS_KEY=%s", cfg.AccessKey),
		fmt.Sprintf("STUPID_RW_SECRET_KEY=%s", cfg.SecretKey),
		fmt.Sprintf("STUPID_BUCKET_NAME=%s", cfg.Bucket),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start sss3: %w", err)
	}

	log.Printf("[storage-sss3] sss3 subprocess started (pid %d, port %d)", cmd.Process.Pid, cfg.Port)

	// Monitor process exit in background
	go func() {
		if err := cmd.Wait(); err != nil && ctx.Err() == nil {
			log.Printf("[storage-sss3] sss3 exited unexpectedly: %v", err)
			os.Exit(1)
		}
	}()

	if err := waitForReady(ctx, cfg.Port); err != nil {
		return fmt.Errorf("sss3 failed to become ready: %w", err)
	}

	log.Printf("[storage-sss3] sss3 is ready on port %d", cfg.Port)
	return nil
}

func waitForReady(ctx context.Context, port int) error {
	url := fmt.Sprintf("http://localhost:%d/healthz", port)
	client := &http.Client{Timeout: 500 * time.Millisecond}

	for i := 0; i < 60; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("sss3 not ready after 15s on port %d", port)
}
