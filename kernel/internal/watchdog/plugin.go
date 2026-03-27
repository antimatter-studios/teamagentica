package watchdog

import (
	"context"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
	"github.com/antimatter-studios/teamagentica/kernel/internal/runtime"
)

// PluginWatchdog periodically scans for plugins whose containers are running
// but have no host/port registration in the kernel. These plugins are alive
// but unreachable via the kernel proxy. Recovery happens via the heartbeat
// re-register signal: when a disconnected plugin sends its next heartbeat,
// the kernel responds with "re-register" instead of "ok", prompting the
// plugin SDK to re-send its full registration.
type PluginWatchdog struct {
	runtime  runtime.ContainerRuntime
	interval time.Duration
	getDB    func() *gorm.DB
}

// NewPluginWatchdog creates a plugin watchdog with the given check interval.
func NewPluginWatchdog(rt runtime.ContainerRuntime, interval time.Duration, getDB func() *gorm.DB) *PluginWatchdog {
	return &PluginWatchdog{
		runtime:  rt,
		interval: interval,
		getDB:    getDB,
	}
}

func (pw *PluginWatchdog) db() *gorm.DB { return pw.getDB() }

// Start runs the plugin watchdog loop until the context is cancelled.
func (pw *PluginWatchdog) Start(ctx context.Context) {
	if pw.runtime == nil {
		log.Println("watchdog/plugin: runtime unavailable, disabled")
		return
	}

	log.Printf("watchdog/plugin: started (interval=%s)", pw.interval)
	ticker := time.NewTicker(pw.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("watchdog/plugin: stopped")
			return
		case <-ticker.C:
			pw.checkDisconnected(ctx)
		}
	}
}

// checkDisconnected finds enabled plugins with a running container but no
// host registration. Logs warnings for observability — the actual recovery
// is triggered via the heartbeat re-register signal (see HeartbeatStatus).
func (pw *PluginWatchdog) checkDisconnected(ctx context.Context) {
	var plugins []models.Plugin
	if err := pw.db().Where("enabled = ? AND host = '' AND container_id != ''", true).Find(&plugins).Error; err != nil {
		return
	}

	for i := range plugins {
		p := &plugins[i]

		running, err := pw.runtime.HealthCheck(ctx, p.ContainerID)
		if err != nil || !running {
			continue
		}

		log.Printf("watchdog/plugin: %s container running but not registered — awaiting re-register via heartbeat", p.ID)
	}
}

// HeartbeatStatus returns the response message for a plugin heartbeat.
// Returns "re-register" if the plugin needs to re-send its full registration
// (host/port was cleared), or "ok" for normal operation.
func HeartbeatStatus(plugin *models.Plugin) string {
	if plugin.Host == "" || plugin.HTTPPort == 0 {
		log.Printf("watchdog/plugin: %s heartbeat received but host/port empty — signalling re-register", plugin.ID)
		return "re-register"
	}
	return "ok"
}
