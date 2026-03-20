package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/antimatter-studios/teamagentica/kernel/internal/auth"
	"github.com/antimatter-studios/teamagentica/kernel/internal/database"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// fetchJWTSecretFromPlugin queries the system-user-manager plugin for its JWT
// secret and initializes the kernel's auth middleware with it. Retries a few
// times since the plugin may still be starting up.
// clientTLS is used for mTLS connections; nil means plain HTTP.
func fetchJWTSecretFromPlugin(clientTLS *tls.Config) error {
	const pluginID = "system-user-manager"

	var plugin models.Plugin
	if err := database.DB.First(&plugin, "id = ?", pluginID).Error; err != nil {
		return fmt.Errorf("plugin %s not in database: %w", pluginID, err)
	}

	if plugin.Host == "" || plugin.HTTPPort == 0 {
		return fmt.Errorf("plugin %s has no host/port", pluginID)
	}

	scheme := "http"
	if clientTLS != nil {
		scheme = "https"
	}

	endpoint := fmt.Sprintf("%s://%s:%d/internal/jwt-secret", scheme, plugin.Host, plugin.HTTPPort)

	transport := &http.Transport{}
	if clientTLS != nil {
		transport.TLSClientConfig = clientTLS
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport}

	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}

		resp, err := client.Get(endpoint)
		if err != nil {
			lastErr = err
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 {
			lastErr = fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
			continue
		}

		var result struct {
			Secret string `json:"secret"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return fmt.Errorf("invalid response: %w", err)
		}

		if result.Secret == "" {
			return fmt.Errorf("empty JWT secret from plugin")
		}

		auth.InitJWT(result.Secret)

		// Cache the secret in the kernel DB for next boot.
		var row models.Config
		if database.DB.Where("owner_id = ? AND key = ?", "kernel", "jwt_secret").First(&row).Error == nil {
			if row.Value != result.Secret {
				database.DB.Model(&row).Update("value", result.Secret)
				log.Printf("jwt: cached secret updated from %s plugin", pluginID)
			} else {
				log.Printf("jwt: secret from %s plugin matches cache", pluginID)
			}
		} else {
			database.DB.Create(&models.Config{
				OwnerID:  "kernel",
				Key:      "jwt_secret",
				Value:    result.Secret,
				IsSecret: true,
			})
			log.Printf("jwt: secret cached from %s plugin (first time)", pluginID)
		}
		return nil
	}

	return fmt.Errorf("gave up after 10 attempts: %w", lastErr)
}
