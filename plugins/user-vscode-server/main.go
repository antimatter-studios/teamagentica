package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load SDK config from env and register with kernel.
	sdkCfg := pluginsdk.LoadConfig()

	pluginID := os.Getenv("TEAMAGENTICA_PLUGIN_ID")
	if pluginID == "" {
		pluginID = "user-vscode-server"
	}

	const defaultPort = 8092

	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           pluginID,
		Host:         getHostname(),
		Port:         defaultPort,
		Capabilities: []string{"workspace:editor"},
		Version:      "1.0.0",
		ConfigSchema: map[string]pluginsdk.ConfigSchemaField{
			"CODE_SERVER_THEME": {
				Type:    "select",
				Label:   "Color Theme",
				Default: "dark",
				Options: []string{"dark", "light"},
				Order:   1,
			},
			"CODE_SERVER_AUTH": {
				Type:    "string",
				Label:   "Auth Mode",
				Default: "none",
				Order:   2,
			},
			"CODE_SERVER_PASSWORD": {
				Type:   "string",
				Label:  "Password",
				Secret: true,
				Order:  3,
			},
			"INFRA_CODE_SERVER_PORT": {
				Type:    "string",
				Label:   "Plugin Port",
				Default: "8092",
				Order:   4,
			},
			"CODE_SERVER_PORT": {
				Type:    "string",
				Label:   "Code Server Port",
				Default: "8443",
				Order:   5,
			},
			"PLUGIN_DEBUG": {
				Type:    "boolean",
				Label:   "Debug Mode",
				Default: "false",
				Order:   99,
			},
		},
	})
	sdkClient.Start(ctx)

	// Fetch plugin config from kernel API.
	pluginConfig, err := sdkClient.FetchConfig()
	if err != nil {
		log.Fatalf("[user-vscode-server] failed to fetch plugin config: %v", err)
	}

	// Extract config values with defaults.
	theme := configOrDefault(pluginConfig, "CODE_SERVER_THEME", "dark")
	auth := configOrDefault(pluginConfig, "CODE_SERVER_AUTH", "none")
	password := pluginConfig["CODE_SERVER_PASSWORD"]
	publicHost := os.Getenv("TEAMAGENTICA_PUBLIC_HOST")
	port := parseIntOrDefault(configOrDefault(pluginConfig, "INFRA_CODE_SERVER_PORT", ""), defaultPort)
	codeServerPort := parseIntOrDefault(configOrDefault(pluginConfig, "CODE_SERVER_PORT", ""), 8443)

	// Build kernel URL for auth validation.
	scheme := "http"
	if sdkCfg.TLSEnabled {
		scheme = "https"
	}
	kernelURL := fmt.Sprintf("%s://%s:%s", scheme, sdkCfg.KernelHost, sdkCfg.KernelPort)

	// Start code-server subprocess.
	csCmd := startCodeServer(codeServerPort, theme, auth, password)

	// Wait for code-server to become ready.
	csTarget := fmt.Sprintf("http://127.0.0.1:%d", codeServerPort)
	waitForReady(csTarget, 30*time.Second)

	// Set up reverse proxy to code-server.
	csURL, _ := url.Parse(csTarget)
	proxy := httputil.NewSingleHostReverseProxy(csURL)

	router := gin.Default()

	router.GET("/health", func(c *gin.Context) {
		// Check if code-server process is still alive.
		if csCmd.ProcessState != nil && csCmd.ProcessState.Exited() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "code-server exited"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Auth middleware — when the plugin has its own subdomain, validate
	// the session cookie or accept a one-time token parameter to bootstrap
	// the cookie (needed because cross-subdomain cookies don't work on .localhost).
	if publicHost != "" {
		router.Use(sessionAuthMiddleware(kernelURL))
	}

	// Proxy everything else to code-server.
	router.NoRoute(func(c *gin.Context) {
		proxy.ServeHTTP(c.Writer, c.Request)
	})

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}

	// Handle signals — forward to code-server, then shut down.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Printf("[user-vscode-server] received %s, shutting down", sig)
		if csCmd.Process != nil {
			csCmd.Process.Signal(sig)
		}
		cancel()
	}()

	pluginsdk.RunWithGracefulShutdown(server, sdkClient)

	// Clean up code-server.
	if csCmd.Process != nil {
		csCmd.Process.Signal(syscall.SIGTERM)
		csCmd.Wait()
	}
}

func startCodeServer(codeServerPort int, theme, auth, password string) *exec.Cmd {
	// Write a config file — the kernel handles auth via session cookies,
	// so code-server's built-in auth is always disabled to prevent it from
	// blocking WebSocket connections through the reverse proxy.
	csConfigDir := "/root/.config/code-server"
	_ = os.MkdirAll(csConfigDir, 0o755)
	csConfig := fmt.Sprintf("bind-addr: 0.0.0.0:%d\nauth: none\ncert: false\n", codeServerPort)
	_ = os.WriteFile(fmt.Sprintf("%s/config.yaml", csConfigDir), []byte(csConfig), 0o644)

	// Write VS Code settings for theme.
	vsTheme := "Default Dark Modern"
	if theme == "light" {
		vsTheme = "Default Light Modern"
	}
	userDir := "/root/.local/share/code-server/User"
	_ = os.MkdirAll(userDir, 0o755)
	settings := fmt.Sprintf("{\n  \"workbench.colorTheme\": %q\n}\n", vsTheme)
	_ = os.WriteFile(fmt.Sprintf("%s/settings.json", userDir), []byte(settings), 0o644)

	args := []string{
		"--disable-telemetry",
		"--disable-update-check",
		"--trusted-origins", "*",
		"/workspaces",
	}

	cmd := exec.Command("code-server", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	env := os.Environ()
	if auth == "password" && password != "" {
		env = append(env, "PASSWORD="+password)
	}
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		log.Fatalf("[user-vscode-server] failed to start code-server: %v", err)
	}

	log.Printf("[user-vscode-server] code-server started (pid %d) on port %d", cmd.Process.Pid, codeServerPort)

	// Monitor subprocess in background — restart on crash.
	go func() {
		for {
			err := cmd.Wait()
			log.Printf("[user-vscode-server] code-server exited: %v — restarting in 2s", err)
			time.Sleep(2 * time.Second)

			newCmd := exec.Command("code-server", args...)
			newCmd.Stdout = os.Stdout
			newCmd.Stderr = os.Stderr
			newCmd.Env = env
			if err := newCmd.Start(); err != nil {
				log.Printf("[user-vscode-server] restart failed: %v", err)
				continue
			}
			log.Printf("[user-vscode-server] code-server restarted (pid %d)", newCmd.Process.Pid)
			cmd = newCmd
		}
	}()

	return cmd
}

func waitForReady(target string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(target + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				log.Printf("[user-vscode-server] code-server is ready")
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	log.Printf("[user-vscode-server] WARNING: code-server not ready after %s, proceeding anyway", timeout)
}

// sessionAuthMiddleware validates the teamagentica_session cookie by calling
// the kernel. Results are cached briefly to avoid hammering the kernel on
// every sub-request (code-server makes many).
func sessionAuthMiddleware(kernelURL string) gin.HandlerFunc {
	type cacheEntry struct {
		valid   bool
		expires time.Time
	}
	var (
		mu    sync.RWMutex
		cache = make(map[string]cacheEntry)
	)

	validate := func(token string) bool {
		mu.RLock()
		if e, ok := cache[token]; ok && time.Now().Before(e.expires) {
			mu.RUnlock()
			return e.valid
		}
		mu.RUnlock()

		req, err := http.NewRequest("GET", kernelURL+"/api/users/me", nil)
		if err != nil {
			return false
		}
		req.Header.Set("Authorization", "Bearer "+token)

		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		valid := resp.StatusCode == http.StatusOK

		mu.Lock()
		cache[token] = cacheEntry{valid: valid, expires: time.Now().Add(30 * time.Second)}
		mu.Unlock()

		return valid
	}

	return func(c *gin.Context) {
		// Check existing cookie first.
		cookie, _ := c.Cookie("teamagentica_session")

		// Accept a one-time token parameter to bootstrap the cookie.
		// This is needed because cross-subdomain cookies don't work on .localhost.
		if cookie == "" {
			if tkn := c.Query("tkn"); tkn != "" {
				if validate(tkn) {
					// Set our own cookie on this subdomain and redirect without the token.
					c.SetSameSite(http.SameSiteLaxMode)
					c.SetCookie("teamagentica_session", tkn, 86400, "/", "", false, true)
					// Redirect to strip the token from the URL.
					q := c.Request.URL.Query()
					q.Del("tkn")
					dest := c.Request.URL.Path
					if encoded := q.Encode(); encoded != "" {
						dest += "?" + encoded
					}
					c.Redirect(http.StatusFound, dest)
					c.Abort()
					return
				}
			}
		}

		if cookie == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}
		if !validate(cookie) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
			return
		}
		c.Next()
	}
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
		return "localhost"
	}
	return hostname
}
