package orchestrator

import (
	"context"
	"log"

	"gorm.io/gorm"

	"roboslop/kernel/internal/config"
	"roboslop/kernel/internal/models"
	"roboslop/kernel/internal/runtime"
)

// Orchestrator manages plugin lifecycle at kernel startup and shutdown.
type Orchestrator struct {
	db      *gorm.DB
	runtime *runtime.DockerRuntime
	config  *config.Config
}

// NewOrchestrator creates a new Orchestrator.
func NewOrchestrator(db *gorm.DB, rt *runtime.DockerRuntime, cfg *config.Config) *Orchestrator {
	return &Orchestrator{
		db:      db,
		runtime: rt,
		config:  cfg,
	}
}

// StartEnabledPlugins queries the DB for all enabled plugins and starts their containers.
// For each plugin:
// 1. Read plugin config from PluginConfig table
// 2. Build env vars map from config
// 3. Inject kernel connection info (ROBOSLOP_KERNEL_HOST, ROBOSLOP_KERNEL_PORT, ROBOSLOP_PLUGIN_ID, ROBOSLOP_PLUGIN_TOKEN)
// 4. Call runtime.PullImage (skip if already present -- don't fail if pull fails, image might be local)
// 5. Call runtime.StartPlugin with env vars
// 6. Update plugin status in DB
// 7. Log result (success or error)
// Continue to next plugin on error (don't let one failed plugin block others).
func (o *Orchestrator) StartEnabledPlugins(ctx context.Context) error {
	if o.runtime == nil {
		log.Println("orchestrator: docker runtime unavailable, skipping boot orchestration")
		return nil
	}

	var plugins []models.Plugin
	if err := o.db.Where("enabled = ?", true).Find(&plugins).Error; err != nil {
		log.Printf("orchestrator: failed to query enabled plugins: %v", err)
		return err
	}

	if len(plugins) == 0 {
		log.Println("orchestrator: no enabled plugins to start")
		return nil
	}

	log.Printf("orchestrator: starting %d enabled plugin(s)", len(plugins))

	for i := range plugins {
		plugin := &plugins[i]

		// Look up service token for this plugin.
		var serviceToken models.ServiceToken
		if err := o.db.Where("name = ? AND revoked = ?", plugin.ID, false).First(&serviceToken).Error; err != nil {
			log.Printf("orchestrator: WARNING: no service token found for plugin %s, skipping (plugin cannot auth without a token)", plugin.ID)
			continue
		}

		// Build env from plugin config.
		env := o.buildEnv(plugin.ID)

		// Inject kernel connection info.
		env["ROBOSLOP_KERNEL_HOST"] = o.config.AdvertiseHost
		env["ROBOSLOP_KERNEL_PORT"] = o.config.Port
		env["ROBOSLOP_PLUGIN_ID"] = plugin.ID

		// Look up the actual JWT token: we stored the hash, but we need
		// to generate a fresh token for the plugin. Service tokens are
		// long-lived JWTs so we regenerate one matching the stored record's
		// capabilities and expiry.
		// NOTE: We can't recover the original token from the hash. The
		// orchestrator generates a fresh short-lived token for the boot session.
		// For persistent tokens, the admin should set ROBOSLOP_PLUGIN_TOKEN
		// in the plugin's config. Here we skip injection if no token is in config.
		if _, hasToken := env["ROBOSLOP_PLUGIN_TOKEN"]; !hasToken {
			log.Printf("orchestrator: WARNING: plugin %s has no ROBOSLOP_PLUGIN_TOKEN in config, service token record exists but original token is not recoverable -- plugin may need manual token configuration", plugin.ID)
		}

		// Stop existing container if still running.
		if plugin.ContainerID != "" {
			_ = o.runtime.StopPlugin(ctx, plugin.ContainerID)
		}

		// Pull image (don't fail if pull fails, image might be local).
		if err := o.runtime.PullImage(ctx, plugin.Image); err != nil {
			log.Printf("orchestrator: pull image %s for plugin %s failed (continuing, image may be local): %v", plugin.Image, plugin.ID, err)
		}

		// Start the container.
		containerID, err := o.runtime.StartPlugin(ctx, plugin, env)
		if err != nil {
			log.Printf("orchestrator: ERROR: failed to start plugin %s: %v", plugin.ID, err)
			o.db.Model(plugin).Updates(map[string]interface{}{
				"container_id": "",
				"status":       "error",
			})
			continue
		}

		// Update plugin status.
		o.db.Model(plugin).Updates(map[string]interface{}{
			"container_id": containerID,
			"status":       "running",
		})

		log.Printf("orchestrator: started plugin %s (container=%s)", plugin.ID, containerID[:12])
	}

	return nil
}

// StopAllPlugins stops all running plugins (used on kernel shutdown).
func (o *Orchestrator) StopAllPlugins(ctx context.Context) error {
	if o.runtime == nil {
		return nil
	}

	var plugins []models.Plugin
	if err := o.db.Where("enabled = ? AND container_id != ?", true, "").Find(&plugins).Error; err != nil {
		log.Printf("orchestrator: failed to query running plugins: %v", err)
		return err
	}

	if len(plugins) == 0 {
		return nil
	}

	log.Printf("orchestrator: stopping %d plugin(s)", len(plugins))

	for i := range plugins {
		plugin := &plugins[i]
		if plugin.ContainerID == "" {
			continue
		}

		if err := o.runtime.StopPlugin(ctx, plugin.ContainerID); err != nil {
			log.Printf("orchestrator: failed to stop plugin %s: %v", plugin.ID, err)
			continue
		}

		o.db.Model(plugin).Updates(map[string]interface{}{
			"container_id": "",
			"status":       "stopped",
		})

		log.Printf("orchestrator: stopped plugin %s", plugin.ID)
	}

	return nil
}

// buildEnv reads PluginConfig rows for a plugin and returns them as a map.
// It also injects default values from the plugin's config_schema for any keys
// that don't have an explicit PluginConfig entry.
func (o *Orchestrator) buildEnv(pluginID string) map[string]string {
	var plugin models.Plugin
	o.db.First(&plugin, "id = ?", pluginID)

	var configs []models.PluginConfig
	o.db.Where("plugin_id = ?", pluginID).Find(&configs)

	env := make(map[string]string, len(configs))
	for _, cfg := range configs {
		env[cfg.Key] = cfg.Value
	}

	// Fill in defaults from config_schema for keys not explicitly set.
	schema, err := plugin.GetConfigSchema()
	if err == nil && schema != nil {
		for key, field := range schema {
			if _, exists := env[key]; !exists && field.Default != "" {
				env[key] = field.Default
			}
		}
	}

	return env
}
