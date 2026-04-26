package orchestrator

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/config"
	"github.com/antimatter-studios/teamagentica/kernel/internal/database"
	"github.com/antimatter-studios/teamagentica/kernel/internal/events"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
	"github.com/antimatter-studios/teamagentica/kernel/internal/runtime"
)

// Orchestrator manages plugin lifecycle at kernel startup and shutdown.
type Orchestrator struct {
	runtime   runtime.ContainerRuntime
	config    *config.Config
	events    *events.Hub
	clientTLS *tls.Config // mTLS config for plugin API calls
}

// NewOrchestrator creates a new Orchestrator.
func NewOrchestrator(rt runtime.ContainerRuntime, cfg *config.Config, hub *events.Hub, clientTLS *tls.Config) *Orchestrator {
	return &Orchestrator{
		runtime:   rt,
		config:    cfg,
		events:    hub,
		clientTLS: clientTLS,
	}
}

// pluginScheme returns "https" if mTLS is configured, "http" otherwise.
func (o *Orchestrator) pluginScheme() string {
	if o.clientTLS != nil {
		return "https"
	}
	return "http"
}

// pluginHTTPClient returns an HTTP client configured for mTLS if available.
func (o *Orchestrator) pluginHTTPClient() *http.Client {
	transport := &http.Transport{}
	if o.clientTLS != nil {
		transport.TLSClientConfig = o.clientTLS
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: transport}
}

func (o *Orchestrator) db() *gorm.DB { return database.Get() }

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
// 3. Inject kernel connection info (TEAMAGENTICA_KERNEL_HOST, TEAMAGENTICA_KERNEL_PORT, TEAMAGENTICA_PLUGIN_TOKEN)
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

	// Auto-enable metadata-only plugins that aren't enabled yet.
	// These have no container to start — they exist purely as discoverable config.
	o.db().Model(&models.Plugin{}).
		Where("image = ? AND enabled = ?", "", false).
		Updates(map[string]interface{}{"enabled": true, "status": "enabled"})

	var plugins []models.Plugin
	if err := o.db().Where("enabled = ?", true).Find(&plugins).Error; err != nil {
		log.Printf("orchestrator: failed to query enabled plugins: %v", err)
		return err
	}

	// Also start any dep plugins declared by enabled plugins that aren't enabled yet.
	// Handles cases where a plugin was enabled before its deps were declared.
	plugins = o.resolveDependencies(plugins)

	if len(plugins) == 0 {
		log.Println("orchestrator: no enabled plugins to start")
		return nil
	}

	log.Printf("orchestrator: starting %d enabled plugin(s)", len(plugins))
	o.emit("orchestrator", "kernel", fmt.Sprintf("boot: starting %d enabled plugin(s)", len(plugins)))

	// Build a capability map: which plugin provides which capability.
	capProviders := map[string]string{} // capability → plugin ID
	for _, p := range plugins {
		for _, cap := range p.GetCapabilities() {
			capProviders[cap] = p.ID
		}
	}

	// Dependency-aware startup queue.
	// Plugins without unmet deps start immediately. Those with unmet deps
	// are pushed to the back of the queue. Once a plugin starts and registers,
	// it satisfies deps for others. If a full pass makes zero progress, we
	// start remaining plugins anyway (best-effort).
	queue := make([]models.Plugin, len(plugins))
	copy(queue, plugins)
	started := map[string]bool{}     // plugin IDs that have started
	healthy := map[string]bool{}     // plugin IDs confirmed healthy (registered)

	for len(queue) > 0 {
		progress := false
		var deferred []models.Plugin

		// Start a batch of plugins whose deps are all satisfied.
		var batch []models.Plugin
		for _, p := range queue {
			if p.IsMetadataOnly() {
				// Metadata-only: mark started immediately.
				o.startPlugin(ctx, &p)
				started[p.ID] = true
				healthy[p.ID] = true
				progress = true
				continue
			}

			deps := p.GetDependencies()
			allMet := true
			for _, dep := range deps {
				providerID, known := capProviders[dep]
				if !known {
					// Unknown dep — can't be satisfied, don't block forever.
					log.Printf("orchestrator: plugin %s depends on %q but no plugin provides it — starting anyway", p.ID, dep)
					continue
				}
				if !healthy[providerID] {
					allMet = false
					break
				}
			}

			if allMet || len(deps) == 0 {
				batch = append(batch, p)
			} else {
				deferred = append(deferred, p)
			}
		}

		// Start the batch in parallel.
		if len(batch) > 0 {
			var wg sync.WaitGroup
			for i := range batch {
				plugin := batch[i]
				wg.Add(1)
				go func() {
					defer wg.Done()
					o.startPlugin(ctx, &plugin)
				}()
			}
			wg.Wait()

			// Wait for batch plugins to register (become healthy).
			for _, p := range batch {
				started[p.ID] = true
				progress = true
				if o.waitForHealthy(ctx, p.ID, 30*time.Second) {
					healthy[p.ID] = true
					log.Printf("orchestrator: plugin %s is healthy", p.ID)
				} else {
					// Treat as healthy anyway so we don't deadlock.
					healthy[p.ID] = true
					log.Printf("orchestrator: plugin %s did not register in time, continuing", p.ID)
				}
			}
		}

		queue = deferred

		// Deadlock detection: if no progress was made, start everything remaining.
		if !progress && len(queue) > 0 {
			log.Printf("orchestrator: dependency deadlock detected, force-starting %d remaining plugin(s)", len(queue))
			var wg sync.WaitGroup
			for i := range queue {
				plugin := queue[i]
				wg.Add(1)
				go func() {
					defer wg.Done()
					o.startPlugin(ctx, &plugin)
				}()
			}
			wg.Wait()
			break
		}
	}

	log.Printf("orchestrator: all %d plugin(s) started", len(plugins))
	return nil
}

// startPlugin handles the full lifecycle of starting a single plugin container.
func (o *Orchestrator) startPlugin(ctx context.Context, plugin *models.Plugin) {
	// Metadata-only plugins have no runtime image — just mark enabled.
	if plugin.IsMetadataOnly() {
		o.db().Model(plugin).Updates(map[string]interface{}{
			"status":  "enabled",
			"enabled": true,
		})
		log.Printf("orchestrator: plugin %s is metadata-only, marked enabled", plugin.ID)
		return
	}

	// Build minimal env — only kernel connection info.
	// Plugin config is fetched via REST API (FetchConfig), not env vars.
	env := map[string]string{
		"TEAMAGENTICA_KERNEL_HOST": o.config.AdvertiseHost,
		"TEAMAGENTICA_KERNEL_PORT": o.config.TLSPort,
		"PLUGIN_ID":                plugin.ID,
	}

	// Stop existing container and clear stale registration data before starting
	// the new container. Clearing host/last_seen here (not after StartPlugin)
	// prevents a race where the new container registers before the DB update runs.
	if plugin.ContainerID != "" || len(plugin.GetContainerIDs()) > 0 {
		o.emit("stop", plugin.ID, fmt.Sprintf("stopping old pod for plugin=%s", plugin.ID))
		_ = o.runtime.StopPluginPod(ctx, plugin)
	}
	o.db().Model(plugin).Updates(map[string]interface{}{
		"container_id": "",
		"status":       "running",
		"host":         "",
		"last_seen":    time.Time{},
	})

	// Ensure declared disks exist and collect host-side paths.
	diskPaths := map[string]string{}
	for _, sd := range plugin.GetSharedDisks() {
		if sd.Name == "" {
			continue
		}
		diskType := sd.Type
		if diskType == "" {
			diskType = "shared"
		}
		path, err := o.ensureDisk(ctx, sd.Name, diskType)
		if err != nil {
			log.Printf("orchestrator: WARNING: failed to ensure disk %q for plugin %s: %v", sd.Name, plugin.ID, err)
			o.emit("warning", plugin.ID, fmt.Sprintf("disk %q: %v", sd.Name, err))
			continue
		}
		// Translate storage-disk internal path to host path.
		// storage-disk returns e.g. "/storage-root/shared/agent-anthropic"
		// Host path is: dataDir + "/storage-disk" + path
		diskPaths[sd.Name] = o.translateDiskPath(path)
	}

	o.emit("start", plugin.ID, fmt.Sprintf("pulling image=%s", plugin.Image))
	if err := o.runtime.PullImage(ctx, plugin.Image); err != nil {
		log.Printf("orchestrator: pull image %s for plugin %s failed (continuing, image may be local): %v", plugin.Image, plugin.ID, err)
		o.emit("warning", plugin.ID, fmt.Sprintf("image pull failed (may be local): %v", err))
	}

	// Start the container.
	o.emit("start", plugin.ID, fmt.Sprintf("starting container image=%s", plugin.Image))
	containerID, err := o.runtime.StartPlugin(ctx, plugin, env, diskPaths)
	if err != nil {
		log.Printf("orchestrator: ERROR: failed to start plugin %s: %v", plugin.ID, err)
		o.emit("error", plugin.ID, fmt.Sprintf("start failed: %v", err))
		o.db().Model(plugin).Updates(map[string]interface{}{
			"container_id": "",
			"status":       "error",
		})
		return
	}

	o.db().Model(plugin).Updates(map[string]interface{}{
		"container_id":  containerID,
		"container_ids": plugin.ContainerIDs,
	})

	log.Printf("orchestrator: started plugin %s (container=%s)", plugin.ID, containerID[:12])
	o.emit("start", plugin.ID, fmt.Sprintf("running container=%s", containerID[:12]))

	// Fire-and-forget: register plugin identity with infra-authz.
	go o.registerPluginIdentity(ctx, plugin)
}

// registerPluginIdentity sends a fire-and-forget POST to infra-authz to register
// a plugin's identity and requested scopes. Non-blocking — errors are logged only.
func (o *Orchestrator) registerPluginIdentity(ctx context.Context, plugin *models.Plugin) {
	scopes := plugin.GetRequestedScopes()
	if len(scopes) == 0 {
		return
	}

	var authz models.Plugin
	if err := o.db().First(&authz, "id = ?", "infra-authz").Error; err != nil {
		return
	}
	if authz.Host == "" || authz.HTTPPort == 0 {
		return
	}

	principal := fmt.Sprintf("plugin:%s", plugin.ID)
	capsType := ""
	caps := plugin.GetCapabilities()
	if len(caps) > 0 {
		capsType = caps[0]
	}
	scopesJSON, _ := json.Marshal(scopes)
	payload := fmt.Sprintf(`{"plugin_id":%q,"principal":%q,"project_id":"default","agent_type":%q,"scopes":%s}`,
		plugin.ID, principal, capsType, string(scopesJSON))

	url := fmt.Sprintf("%s://%s:%d/identity/register", o.pluginScheme(), authz.Host, authz.HTTPPort)
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.pluginHTTPClient().Do(req)
	if err != nil {
		log.Printf("orchestrator: failed to register identity for %s with infra-authz: %v", plugin.ID, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		log.Printf("orchestrator: registered identity for %s with infra-authz (scopes=%v)", plugin.ID, scopes)
	} else {
		log.Printf("orchestrator: infra-authz returned %d for identity registration of %s", resp.StatusCode, plugin.ID)
	}
}

// translateDiskPath converts a storage-disk internal path (e.g. "/data/storage-root/shared/agent-anthropic")
// to a host-side path for Docker bind mounting.
// storage-disk's /data maps to host's {DataDir}/storage-disk, so we strip the /data prefix.
func (o *Orchestrator) translateDiskPath(storagePath string) string {
	// Strip the leading /data/ prefix — storage-disk's /data = host's {DataDir}/storage-disk
	cleaned := strings.TrimPrefix(storagePath, "/data/")
	return filepath.Join(o.config.DataDir, "storage-disk", cleaned)
}

// ensureDisk calls storage-disk's API to get-or-create a disk and returns
// the path (from storage-disk's perspective, e.g. "/storage-root/shared/agent-anthropic").
// The kernel translates this to a host-side path for bind mounting.
func (o *Orchestrator) ensureDisk(ctx context.Context, diskName, diskType string) (string, error) {
	// Find storage-disk plugin address.
	var storageDisk models.Plugin
	if err := o.db().First(&storageDisk, "id = ?", "storage-disk").Error; err != nil {
		return "", fmt.Errorf("storage-disk plugin not found: %w", err)
	}
	if storageDisk.Host == "" || storageDisk.HTTPPort == 0 {
		return "", fmt.Errorf("storage-disk plugin not ready (host=%q port=%d)", storageDisk.Host, storageDisk.HTTPPort)
	}

	baseURL := fmt.Sprintf("%s://%s:%d", o.pluginScheme(), storageDisk.Host, storageDisk.HTTPPort)
	client := o.pluginHTTPClient()

	// Try to create; 409 means it already exists.
	createBody := fmt.Sprintf(`{"name":%q,"type":%q}`, diskName, diskType)
	createReq, _ := http.NewRequestWithContext(ctx, "POST", baseURL+"/disks", strings.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(createReq)
	if err != nil {
		return "", fmt.Errorf("request to storage-disk failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Path string `json:"path"`
	}

	if resp.StatusCode == http.StatusCreated {
		json.NewDecoder(resp.Body).Decode(&result)
		log.Printf("orchestrator: created %s disk %q (path=%s)", diskType, diskName, result.Path)
		return result.Path, nil
	}

	if resp.StatusCode == http.StatusConflict {
		// Already exists — fetch the path.
		pathReq, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/disks/%s/%s/path", baseURL, diskType, diskName), nil)
		pathResp, err := client.Do(pathReq)
		if err != nil {
			return "", fmt.Errorf("failed to get disk path: %w", err)
		}
		defer pathResp.Body.Close()
		json.NewDecoder(pathResp.Body).Decode(&result)
		log.Printf("orchestrator: %s disk %q already exists (path=%s)", diskType, diskName, result.Path)
		return result.Path, nil
	}

	return "", fmt.Errorf("storage-disk returned %d for disk %q", resp.StatusCode, diskName)
}

// translateDiskMounts converts pre-resolved DiskMount entries (with SourcePath)
// into host-side bind mount paths. No storage-disk API calls needed.
func (o *Orchestrator) translateDiskMounts(mounts []models.DiskMount) []runtime.ResolvedDiskMount {
	var resolved []runtime.ResolvedDiskMount
	for _, dm := range mounts {
		if dm.SourcePath == "" || dm.Target == "" {
			continue
		}
		hostPath := o.translateDiskPath(dm.SourcePath)
		resolved = append(resolved, runtime.ResolvedDiskMount{
			HostPath: hostPath,
			Target:   dm.Target,
			ReadOnly: dm.ReadOnly,
		})
	}
	return resolved
}

// waitForHealthy polls the DB until a plugin has registered (host is set)
// or the timeout expires. Returns true if the plugin registered in time.
func (o *Orchestrator) waitForHealthy(ctx context.Context, pluginID string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var p models.Plugin
		if err := o.db().First(&p, "id = ?", pluginID).Error; err == nil {
			if p.Host != "" {
				return true
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
	return false
}

// RestartPlugin restarts a single enabled plugin by ID.
// Used by the health monitor to auto-recover disappeared containers.
func (o *Orchestrator) RestartPlugin(ctx context.Context, pluginID string) error {
	if o.runtime == nil {
		return fmt.Errorf("docker runtime unavailable")
	}

	var plugin models.Plugin
	if err := o.db().First(&plugin, "id = ?", pluginID).Error; err != nil {
		return fmt.Errorf("plugin not found: %w", err)
	}

	if !plugin.Enabled {
		return fmt.Errorf("plugin %s is not enabled", pluginID)
	}

	o.emit("restart", pluginID, "auto-restart triggered")

	// Stop existing pod (api + sidecars) if still around.
	if plugin.ContainerID != "" || len(plugin.GetContainerIDs()) > 0 {
		o.emit("stop", pluginID, "stopping pod for plugin="+pluginID)
		_ = o.runtime.StopPluginPod(ctx, &plugin)
	}

	env := map[string]string{
		"TEAMAGENTICA_KERNEL_HOST": o.config.AdvertiseHost,
		"TEAMAGENTICA_KERNEL_PORT": o.config.TLSPort,
		"PLUGIN_ID":                plugin.ID,
	}

	// Resolve shared disk paths.
	diskPaths := map[string]string{}
	for _, sd := range plugin.GetSharedDisks() {
		if sd.Name == "" {
			continue
		}
		diskType := sd.Type
		if diskType == "" {
			diskType = "shared"
		}
		if path, err := o.ensureDisk(ctx, sd.Name, diskType); err == nil {
			diskPaths[sd.Name] = o.translateDiskPath(path)
		} else {
			log.Printf("orchestrator: WARNING: restart: failed to ensure disk %q: %v", sd.Name, err)
		}
	}

	o.emit("start", pluginID, fmt.Sprintf("starting container image=%s", plugin.Image))
	containerID, err := o.runtime.StartPlugin(ctx, &plugin, env, diskPaths)
	if err != nil {
		o.emit("error", pluginID, fmt.Sprintf("restart failed: %v", err))
		o.db().Model(&plugin).Updates(map[string]interface{}{
			"container_id": "",
			"status":       "error",
		})
		return fmt.Errorf("failed to start plugin %s: %w", pluginID, err)
	}

	o.db().Model(&plugin).Updates(map[string]interface{}{
		"container_id":  containerID,
		"container_ids": plugin.ContainerIDs,
		"status":        "running",
		"host":          "",
		"last_seen":     time.Time{},
	})

	log.Printf("orchestrator: auto-restarted plugin %s (container=%s)", pluginID, containerID[:12])
	o.emit("restart", pluginID, fmt.Sprintf("running container=%s", containerID[:12]))
	return nil
}

// MigrateDiskMountsToSourcePath is a one-time migration that converts managed
// container disk_mounts from the old format (disk_id) to the new format (source_path).
// It resolves each disk_id via storage-disk's API and rewrites the record.
// Records that already have source_path are skipped.
func (o *Orchestrator) MigrateDiskMountsToSourcePath(ctx context.Context) {
	var containers []models.ManagedContainer
	if err := o.db().Find(&containers).Error; err != nil {
		log.Printf("orchestrator: migration: failed to query managed containers: %v", err)
		return
	}

	// Find storage-disk plugin for resolution.
	var storageDisk models.Plugin
	if err := o.db().First(&storageDisk, "id = ?", "storage-disk").Error; err != nil {
		log.Printf("orchestrator: migration: storage-disk not found, skipping disk_mounts migration")
		return
	}
	if storageDisk.Host == "" || storageDisk.HTTPPort == 0 {
		log.Printf("orchestrator: migration: storage-disk not ready, skipping disk_mounts migration")
		return
	}

	baseURL := fmt.Sprintf("%s://%s:%d", o.pluginScheme(), storageDisk.Host, storageDisk.HTTPPort)
	client := o.pluginHTTPClient()

	for i := range containers {
		mc := &containers[i]
		mounts := mc.GetDiskMounts()
		if len(mounts) == 0 {
			continue
		}

		// Check if any mount needs migration (has empty SourcePath).
		needsMigration := false
		for _, dm := range mounts {
			if dm.SourcePath == "" && dm.Target != "" {
				needsMigration = true
				break
			}
		}
		if !needsMigration {
			continue
		}

		// Read raw JSON to extract old disk_id values.
		var rawMounts []struct {
			DiskID     string `json:"disk_id"`
			SourcePath string `json:"source_path"`
			Target     string `json:"target"`
			ReadOnly   bool   `json:"read_only,omitempty"`
		}
		if err := json.Unmarshal([]byte(mc.DiskMounts), &rawMounts); err != nil {
			continue
		}

		migrated := make([]models.DiskMount, 0, len(rawMounts))
		for _, rm := range rawMounts {
			if rm.SourcePath != "" {
				// Already migrated.
				migrated = append(migrated, models.DiskMount{
					SourcePath: rm.SourcePath,
					Target:     rm.Target,
					ReadOnly:   rm.ReadOnly,
				})
				continue
			}
			if rm.DiskID == "" {
				continue
			}

			// Resolve disk_id → path via storage-disk.
			req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/disks/by-id/%s", baseURL, rm.DiskID), nil)
			resp, err := client.Do(req)
			if err != nil {
				log.Printf("orchestrator: migration: failed to resolve disk %s for container %s: %v", rm.DiskID, mc.ID, err)
				continue
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				log.Printf("orchestrator: migration: storage-disk returned %d for disk %s", resp.StatusCode, rm.DiskID)
				continue
			}

			var result struct{ Path string `json:"path"` }
			json.NewDecoder(resp.Body).Decode(&result)

			migrated = append(migrated, models.DiskMount{
				SourcePath: result.Path,
				Target:     rm.Target,
				ReadOnly:   rm.ReadOnly,
			})
			log.Printf("orchestrator: migration: disk %s → %s (container %s)", rm.DiskID, result.Path, mc.ID)
		}

		mc.SetDiskMounts(migrated)
		o.db().Model(mc).Update("disk_mounts", mc.DiskMounts)
		log.Printf("orchestrator: migration: migrated disk_mounts for container %s (%s)", mc.ID, mc.Name)
	}
}

// RecoverManagedContainers recreates managed containers that were running
// before the kernel restarted. Docker containers don't survive host restart,
// so we recreate them from the stored config.
func (o *Orchestrator) RecoverManagedContainers(ctx context.Context) {
	if o.runtime == nil {
		return
	}

	var containers []models.ManagedContainer
	if err := o.db().Where("status = ?", "running").Find(&containers).Error; err != nil {
		log.Printf("orchestrator: failed to query managed containers: %v", err)
		return
	}

	if len(containers) == 0 {
		return
	}

	log.Printf("orchestrator: recovering %d managed container(s)", len(containers))

	for i := range containers {
		mc := &containers[i]
		resolvedMounts := o.translateDiskMounts(mc.GetDiskMounts())
		containerID, err := o.runtime.StartManagedContainer(ctx, mc, o.config.BaseDomain, resolvedMounts)
		if err != nil {
			log.Printf("orchestrator: failed to recover managed container %s: %v", mc.ID, err)
			o.db().Model(mc).Update("status", "stopped")
			continue
		}
		o.db().Model(mc).Update("container_id", containerID)
		log.Printf("orchestrator: recovered managed container %s (%s)", mc.ID, mc.Name)
	}
}

// StopAllPlugins stops all running plugins and managed containers (used on kernel shutdown).
func (o *Orchestrator) StopAllPlugins(ctx context.Context) error {
	if o.runtime == nil {
		return nil
	}

	var plugins []models.Plugin
	if err := o.db().Where("enabled = ? AND container_id != ?", true, "").Find(&plugins).Error; err != nil {
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

		o.emit("stop", plugin.ID, "stopping pod for plugin="+plugin.ID)
		if err := o.runtime.StopPluginPod(ctx, plugin); err != nil {
			log.Printf("orchestrator: failed to stop pod for plugin %s: %v", plugin.ID, err)
			o.emit("error", plugin.ID, fmt.Sprintf("stop failed: %v", err))
			continue
		}

		o.db().Model(plugin).Updates(map[string]interface{}{
			"container_id": "",
			"status":       "stopped",
		})

		log.Printf("orchestrator: stopped plugin %s", plugin.ID)
		o.emit("stop", plugin.ID, "stopped")
	}

	// Stop managed containers.
	var mcList []models.ManagedContainer
	if err := o.db().Where("status = ? AND container_id != ?", "running", "").Find(&mcList).Error; err == nil {
		for i := range mcList {
			mc := &mcList[i]
			_ = o.runtime.StopPlugin(ctx, mc.ContainerID)
			o.db().Model(mc).Updates(map[string]interface{}{
				"container_id": "",
				"status":       "stopped",
			})
		}
		if len(mcList) > 0 {
			log.Printf("orchestrator: stopped %d managed container(s)", len(mcList))
		}
	}

	return nil
}

// resolveDependencies expands a plugin list with any dep plugins not yet enabled.
// Dep plugins are marked enabled in the DB so they persist across restarts.
func (o *Orchestrator) resolveDependencies(plugins []models.Plugin) []models.Plugin {
	seen := map[string]bool{}
	for _, p := range plugins {
		seen[p.ID] = true
	}

	var allPlugins []models.Plugin
	o.db().Find(&allPlugins)

	capMap := map[string]*models.Plugin{}
	for i := range allPlugins {
		for _, c := range allPlugins[i].GetCapabilities() {
			capMap[c] = &allPlugins[i]
		}
	}

	result := make([]models.Plugin, len(plugins))
	copy(result, plugins)

	for _, p := range plugins {
		for _, cap := range p.GetDependencies() {
			dep, ok := capMap[cap]
			if !ok || seen[dep.ID] {
				continue
			}
			seen[dep.ID] = true
			o.db().Model(dep).Updates(map[string]interface{}{"enabled": true})
			log.Printf("orchestrator: auto-enabling dep %s (provides %q required by %s)", dep.ID, cap, p.ID)
			result = append(result, *dep)
		}
	}
	return result
}

