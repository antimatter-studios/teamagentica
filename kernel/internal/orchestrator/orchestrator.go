package orchestrator

import (
	"context"
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/config"
	"github.com/antimatter-studios/teamagentica/kernel/internal/events"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
	"github.com/antimatter-studios/teamagentica/kernel/internal/runtime"
)

// Orchestrator manages plugin lifecycle at kernel startup and shutdown.
type Orchestrator struct {
	db      *gorm.DB
	runtime *runtime.DockerRuntime
	config  *config.Config
	events  *events.Hub
}

// NewOrchestrator creates a new Orchestrator.
func NewOrchestrator(db *gorm.DB, rt *runtime.DockerRuntime, cfg *config.Config, hub *events.Hub) *Orchestrator {
	return &Orchestrator{
		db:      db,
		runtime: rt,
		config:  cfg,
		events:  hub,
	}
}

// emit sends a debug event if the events hub is available.
func (o *Orchestrator) emit(eventType, pluginID, detail string) {
	if o.events != nil {
		o.events.Emit(events.DebugEvent{
			Type:     eventType,
			PluginID: pluginID,
			Detail:   detail,
		})
	}
}

// StartEnabledPlugins queries the DB for all enabled plugins and starts their containers.
// For each plugin:
// 1. Read plugin config from PluginConfig table
// 2. Build env vars map from config
// 3. Inject kernel connection info (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_KERNEL_PORT, TEAMAGENTICA_PLUGIN_ID, TEAMAGENTICA_PLUGIN_TOKEN)
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
	o.emit("orchestrator", "kernel", fmt.Sprintf("boot: starting %d enabled plugin(s)", len(plugins)))

	for i := range plugins {
		plugin := &plugins[i]

		// Look up service token for this plugin.
		var serviceToken models.ServiceToken
		if err := o.db.Where("name = ? AND revoked = ?", plugin.ID, false).First(&serviceToken).Error; err != nil {
			log.Printf("orchestrator: WARNING: no service token found for plugin %s, skipping (plugin cannot auth without a token)", plugin.ID)
			o.emit("warning", plugin.ID, "no service token found, skipping")
			continue
		}

		// Build env from plugin config.
		env := o.buildEnv(plugin.ID)

		// Inject kernel connection info.
		env["TEAMAGENTICA_KERNEL_HOST"] = o.config.AdvertiseHost
		env["TEAMAGENTICA_KERNEL_PORT"] = o.config.Port
		env["TEAMAGENTICA_PLUGIN_ID"] = plugin.ID

		// Inject the service token stored on the plugin record.
		if plugin.ServiceToken != "" {
			env["TEAMAGENTICA_PLUGIN_TOKEN"] = plugin.ServiceToken
		} else {
			log.Printf("orchestrator: WARNING: plugin %s has no service token — plugin cannot authenticate with kernel", plugin.ID)
			o.emit("warning", plugin.ID, "no service token on plugin record")
		}

		// Stop existing container if still running.
		if plugin.ContainerID != "" {
			o.emit("stop", plugin.ID, fmt.Sprintf("stopping old container=%s", plugin.ContainerID[:12]))
			_ = o.runtime.StopPlugin(ctx, plugin.ContainerID)
		}

		// Resolve image tag for dev mode.
		plugin.Image = o.config.ResolveImage(plugin.Image)

		// Pull image (don't fail if pull fails, image might be local).
		o.emit("start", plugin.ID, fmt.Sprintf("pulling image=%s", plugin.Image))
		if err := o.runtime.PullImage(ctx, plugin.Image); err != nil {
			log.Printf("orchestrator: pull image %s for plugin %s failed (continuing, image may be local): %v", plugin.Image, plugin.ID, err)
			o.emit("warning", plugin.ID, fmt.Sprintf("image pull failed (may be local): %v", err))
		}

		// Start the container.
		o.emit("start", plugin.ID, fmt.Sprintf("starting container image=%s", plugin.Image))
		containerID, err := o.runtime.StartPlugin(ctx, plugin, env)
		if err != nil {
			log.Printf("orchestrator: ERROR: failed to start plugin %s: %v", plugin.ID, err)
			o.emit("error", plugin.ID, fmt.Sprintf("start failed: %v", err))
			o.db.Model(plugin).Updates(map[string]interface{}{
				"container_id": "",
				"status":       "error",
			})
			continue
		}

		// Update plugin status and clear stale registration data so the health
		// monitor uses Docker-level checks (not heartbeat staleness) while the
		// plugin is still building/booting.
		o.db.Model(plugin).Updates(map[string]interface{}{
			"container_id": containerID,
			"status":       "running",
			"host":         "",
			"last_seen":    time.Time{},
		})

		log.Printf("orchestrator: started plugin %s (container=%s)", plugin.ID, containerID[:12])
		o.emit("start", plugin.ID, fmt.Sprintf("running container=%s", containerID[:12]))
	}

	return nil
}

// RestartPlugin restarts a single enabled plugin by ID.
// Used by the health monitor to auto-recover disappeared containers.
func (o *Orchestrator) RestartPlugin(ctx context.Context, pluginID string) error {
	if o.runtime == nil {
		return fmt.Errorf("docker runtime unavailable")
	}

	var plugin models.Plugin
	if err := o.db.First(&plugin, "id = ?", pluginID).Error; err != nil {
		return fmt.Errorf("plugin not found: %w", err)
	}

	if !plugin.Enabled {
		return fmt.Errorf("plugin %s is not enabled", pluginID)
	}

	o.emit("restart", pluginID, "auto-restart triggered")

	// Stop existing container if still around.
	if plugin.ContainerID != "" {
		o.emit("stop", pluginID, fmt.Sprintf("stopping container=%s", plugin.ContainerID[:12]))
		_ = o.runtime.StopPlugin(ctx, plugin.ContainerID)
	}

	// Build env.
	env := o.buildEnv(pluginID)
	env["TEAMAGENTICA_KERNEL_HOST"] = o.config.AdvertiseHost
	env["TEAMAGENTICA_KERNEL_PORT"] = o.config.Port
	env["TEAMAGENTICA_PLUGIN_ID"] = plugin.ID

	if plugin.ServiceToken != "" {
		env["TEAMAGENTICA_PLUGIN_TOKEN"] = plugin.ServiceToken
	}

	plugin.Image = o.config.ResolveImage(plugin.Image)

	o.emit("start", pluginID, fmt.Sprintf("starting container image=%s", plugin.Image))
	containerID, err := o.runtime.StartPlugin(ctx, &plugin, env)
	if err != nil {
		o.emit("error", pluginID, fmt.Sprintf("restart failed: %v", err))
		o.db.Model(&plugin).Updates(map[string]interface{}{
			"container_id": "",
			"status":       "error",
		})
		return fmt.Errorf("failed to start plugin %s: %w", pluginID, err)
	}

	o.db.Model(&plugin).Updates(map[string]interface{}{
		"container_id": containerID,
		"status":       "running",
		"host":         "",
		"last_seen":    time.Time{},
	})

	log.Printf("orchestrator: auto-restarted plugin %s (container=%s)", pluginID, containerID[:12])
	o.emit("restart", pluginID, fmt.Sprintf("running container=%s", containerID[:12]))
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
	o.emit("orchestrator", "kernel", fmt.Sprintf("shutdown: stopping %d plugin(s)", len(plugins)))

	for i := range plugins {
		plugin := &plugins[i]
		if plugin.ContainerID == "" {
			continue
		}

		o.emit("stop", plugin.ID, fmt.Sprintf("stopping container=%s", plugin.ContainerID[:12]))
		if err := o.runtime.StopPlugin(ctx, plugin.ContainerID); err != nil {
			log.Printf("orchestrator: failed to stop plugin %s: %v", plugin.ID, err)
			o.emit("error", plugin.ID, fmt.Sprintf("stop failed: %v", err))
			continue
		}

		o.db.Model(plugin).Updates(map[string]interface{}{
			"container_id": "",
			"status":       "stopped",
		})

		log.Printf("orchestrator: stopped plugin %s", plugin.ID)
		o.emit("stop", plugin.ID, "stopped")
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
