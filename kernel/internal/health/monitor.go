package health

import (
	"context"
	"log"
	"time"

	"gorm.io/gorm"

	"roboslop/kernel/internal/models"
	"roboslop/kernel/internal/runtime"
)

// Monitor periodically checks the health of running plugin containers.
type Monitor struct {
	db       *gorm.DB
	runtime  *runtime.DockerRuntime
	interval time.Duration

	// unhealthyCounts tracks consecutive unhealthy checks per plugin ID.
	unhealthyCounts map[string]int
}

// NewMonitor creates a health monitor with the given check interval.
func NewMonitor(db *gorm.DB, rt *runtime.DockerRuntime, interval time.Duration) *Monitor {
	return &Monitor{
		db:              db,
		runtime:         rt,
		interval:        interval,
		unhealthyCounts: make(map[string]int),
	}
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
	var plugins []models.Plugin
	if err := m.db.Where("enabled = ?", true).Find(&plugins).Error; err != nil {
		log.Printf("health monitor: query plugins: %v", err)
		return
	}

	heartbeatTimeout := 90 * time.Second

	for i := range plugins {
		p := &plugins[i]

		// Check heartbeat-based health: if a plugin has registered (has a Host)
		// but hasn't sent a heartbeat within the timeout, mark it unhealthy.
		if p.Host != "" && !p.LastSeen.IsZero() {
			if time.Since(p.LastSeen) > heartbeatTimeout {
				log.Printf("health monitor: plugin %s heartbeat stale (last seen %s ago)", p.ID, time.Since(p.LastSeen).Round(time.Second))
				m.setStatus(p, "unhealthy")
				m.unhealthyCounts[p.ID]++
				if m.unhealthyCounts[p.ID] >= 3 {
					log.Printf("WARNING: plugin %s has been unhealthy %d consecutive checks", p.ID, m.unhealthyCounts[p.ID])
				}
				continue
			}
		}

		// Docker container health check (existing behaviour).
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
			m.setStatus(p, "unhealthy")
			m.unhealthyCounts[p.ID]++
		}

		if m.unhealthyCounts[p.ID] >= 3 {
			log.Printf("WARNING: plugin %s has been unhealthy %d consecutive checks", p.ID, m.unhealthyCounts[p.ID])
		}
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
