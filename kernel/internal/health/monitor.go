package health

import (
	"context"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/events"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
	"github.com/antimatter-studios/teamagentica/kernel/internal/runtime"
)

// Restarter can restart a plugin by ID. Implemented by the orchestrator.
type Restarter interface {
	RestartPlugin(ctx context.Context, pluginID string) error
}

// Monitor periodically checks the health of running plugin containers.
type Monitor struct {
	db        *gorm.DB
	runtime   *runtime.DockerRuntime
	events    *events.Hub
	interval  time.Duration
	restarter Restarter

	// unhealthyCounts tracks consecutive unhealthy checks per plugin ID.
	unhealthyCounts map[string]int
}

// restartThreshold is the number of consecutive unhealthy checks before
// the monitor attempts an auto-restart (~2 minutes at 30s interval).
const restartThreshold = 4

// NewMonitor creates a health monitor with the given check interval.
func NewMonitor(db *gorm.DB, rt *runtime.DockerRuntime, evts *events.Hub, interval time.Duration) *Monitor {
	return &Monitor{
		db:              db,
		runtime:         rt,
		events:          evts,
		interval:        interval,
		unhealthyCounts: make(map[string]int),
	}
}

// SetRestarter attaches a restarter (typically the orchestrator) for auto-recovery.
func (m *Monitor) SetRestarter(r Restarter) {
	m.restarter = r
}

// Start runs the health-check loop until the context is cancelled.
func (m *Monitor) Start(ctx context.Context) {
	if m.runtime == nil {
		log.Println("health monitor: docker runtime unavailable, monitor disabled")
		return
	}

	log.Printf("health monitor started (interval=%s)", m.interval)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("health monitor stopped")
			return
		case <-ticker.C:
			m.checkAll(ctx)
		}
	}
}

func (m *Monitor) checkAll(ctx context.Context) {
	m.checkPlugins(ctx)
	m.checkManagedContainers(ctx)
}

func (m *Monitor) checkPlugins(ctx context.Context) {
	var plugins []models.Plugin
	if err := m.db.Where("enabled = ?", true).Find(&plugins).Error; err != nil {
		log.Printf("health monitor: query plugins: %v", err)
		return
	}

	heartbeatTimeout := 90 * time.Second

	for i := range plugins {
		p := &plugins[i]

		// Metadata-only plugins have no container — skip health checks entirely.
		if p.IsMetadataOnly() {
			continue
		}

		// Case 1: Plugin has no container at all (never started, or previous start failed).
		// Attempt to start it if we have a restarter.
		if p.ContainerID == "" && p.Status != "running" {
			m.unhealthyCounts[p.ID]++
			if m.unhealthyCounts[p.ID] >= restartThreshold && m.restarter != nil {
				log.Printf("health monitor: plugin %s has no container — attempting start", p.ID)
				m.emitEvent(p.ID, "warning", "no container found — attempting auto-start")
				if err := m.restarter.RestartPlugin(ctx, p.ID); err != nil {
					log.Printf("health monitor: auto-start failed for %s: %v", p.ID, err)
					m.emitEvent(p.ID, "error", "auto-start failed: "+err.Error())
				} else {
					m.emitEvent(p.ID, "info", "auto-start succeeded")
					m.unhealthyCounts[p.ID] = 0
				}
			}
			continue
		}

		// Case 2: Plugin registered and has heartbeats — check for staleness.
		if p.Host != "" && !p.LastSeen.IsZero() {
			if time.Since(p.LastSeen) > heartbeatTimeout {
				m.unhealthyCounts[p.ID]++
				log.Printf("health monitor: plugin %s heartbeat stale (last seen %s ago, count=%d)",
					p.ID, time.Since(p.LastSeen).Round(time.Second), m.unhealthyCounts[p.ID])

				// Check if container is still running.
				if p.ContainerID != "" {
					running, err := m.runtime.HealthCheck(ctx, p.ContainerID)
					if err != nil || !running {
						log.Printf("health monitor: plugin %s container gone", p.ID)
						m.db.Model(p).Updates(map[string]interface{}{
							"status": "stopped",
							"host":   "",
						})
						m.tryRestart(ctx, p)
						continue
					}
				}

				m.setStatus(p, "unhealthy")

				// After enough unhealthy checks, force restart even if container appears running
				// (it may be stuck/unresponsive).
				if m.unhealthyCounts[p.ID] >= restartThreshold && m.restarter != nil {
					log.Printf("health monitor: plugin %s stuck (heartbeat stale, container present) — force restart", p.ID)
					m.emitEvent(p.ID, "warning", "plugin unresponsive — force restart")
					m.db.Model(p).Updates(map[string]interface{}{
						"status": "stopped",
						"host":   "",
					})
					m.tryRestart(ctx, p)
				}
				continue
			}
		}

		// Case 3: Docker container health check for plugins that haven't registered yet
		// (e.g. still booting) or that have a healthy heartbeat.
		if p.ContainerID == "" {
			continue
		}

		running, err := m.runtime.HealthCheck(ctx, p.ContainerID)
		if err != nil {
			m.setStatus(p, "error")
			m.unhealthyCounts[p.ID]++
		} else if running {
			m.setStatus(p, "running")
			m.unhealthyCounts[p.ID] = 0
		} else {
			m.unhealthyCounts[p.ID]++
			m.setStatus(p, "unhealthy")

			// Container exited/crashed — auto-restart.
			if m.unhealthyCounts[p.ID] >= restartThreshold {
				log.Printf("health monitor: plugin %s container not running after %d checks — auto-restart",
					p.ID, m.unhealthyCounts[p.ID])
				m.db.Model(p).Updates(map[string]interface{}{
					"status": "stopped",
					"host":   "",
				})
				m.tryRestart(ctx, p)
			}
		}
	}
}

// tryRestart attempts to restart a plugin and resets the unhealthy counter on success.
func (m *Monitor) tryRestart(ctx context.Context, p *models.Plugin) {
	if m.restarter == nil {
		m.emitEvent(p.ID, "warning", "container gone — no restarter available")
		return
	}

	m.emitEvent(p.ID, "warning", "attempting auto-restart")
	if err := m.restarter.RestartPlugin(ctx, p.ID); err != nil {
		log.Printf("health monitor: auto-restart failed for %s: %v", p.ID, err)
		m.emitEvent(p.ID, "error", "auto-restart failed: "+err.Error())
	} else {
		log.Printf("health monitor: auto-restarted %s", p.ID)
		m.emitEvent(p.ID, "info", "auto-restart succeeded")
		m.unhealthyCounts[p.ID] = 0
	}
}

func (m *Monitor) emitEvent(pluginID, eventType, detail string) {
	if m.events != nil {
		m.events.Emit(events.DebugEvent{
			Type:     eventType,
			PluginID: pluginID,
			Detail:   detail,
		})
	}
}

func (m *Monitor) setStatus(p *models.Plugin, status string) {
	if p.Status == status {
		return
	}
	if err := m.db.Model(p).Update("status", status).Error; err != nil {
		log.Printf("health monitor: update status for %s: %v", p.ID, err)
	}
}

// checkManagedContainers verifies Docker state for running managed containers.
// No auto-restart — the owning plugin decides whether to recreate.
func (m *Monitor) checkManagedContainers(ctx context.Context) {
	var containers []models.ManagedContainer
	if err := m.db.Where("status = ?", "running").Find(&containers).Error; err != nil {
		log.Printf("health monitor: query managed containers: %v", err)
		return
	}

	for i := range containers {
		mc := &containers[i]
		if mc.ContainerID == "" {
			continue
		}

		running, err := m.runtime.HealthCheck(ctx, mc.ContainerID)
		if err != nil || !running {
			log.Printf("health monitor: managed container %s (%s) no longer running", mc.ID, mc.Name)
			m.db.Model(mc).Update("status", "stopped")
			m.emitEvent(mc.PluginID, "warning", "managed container "+mc.Name+" stopped")
		}
	}
}
