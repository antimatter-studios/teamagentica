package storage

import (
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Job struct {
	ID           string `gorm:"primaryKey" json:"id"`
	Name         string `json:"name"`
	Text         string `json:"text"`
	Type         string `json:"type"`          // "once" | "repeat"
	Schedule     string `json:"schedule"`      // "5m" or "*/5 * * * *"
	ScheduleType string `json:"schedule_type"` // "cron" | "interval"
	TriggerType  string `json:"trigger_type"`  // "timer" | "event"
	EventPattern string `json:"event_pattern"` // e.g. "task-tracking:assign"
	ActionType   string `json:"action_type"`   // "log" for now
	ActionConfig string `json:"action_config"` // JSON blob for future use
	Enabled      bool   `json:"enabled"`
	FiredCount   int    `json:"fired_count"`
	NextFire     int64  `json:"next_fire"` // unix ms
	CreatedAt    int64  `gorm:"autoCreateTime:milli" json:"created_at"`
	UpdatedAt    int64  `gorm:"autoUpdateTime:milli" json:"updated_at"`
}

type ExecutionLog struct {
	ID      string `gorm:"primaryKey" json:"id"`
	JobID   string `gorm:"index" json:"job_id"`
	JobName string `json:"job_name"`
	Text    string `json:"text"`
	Result  string `json:"result"`
	FiredAt int64  `gorm:"autoCreateTime:milli" json:"fired_at"`
}

type DispatchEntry struct {
	ID             string `gorm:"primaryKey" json:"id"`
	CardID         string `gorm:"index" json:"card_id"`
	BoardID        string `json:"board_id"`
	CardTitle      string `json:"card_title"`
	AgentAlias     string `gorm:"index" json:"agent_alias"`
	Status         string `gorm:"index" json:"status"` // "pending" | "dispatched" | "completed" | "failed"
	DispatchType   string `json:"dispatch_type"`        // "triage" | "reply"
	TriggerComment string `json:"trigger_comment"`      // user comment body that triggered this (empty for assign)
	ErrorMessage   string `json:"error_message,omitempty"`
	AgentResponse  string `json:"agent_response,omitempty"` // truncated response
	CreatedAt      int64  `gorm:"autoCreateTime:milli" json:"created_at"`
	DispatchedAt   int64  `json:"dispatched_at,omitempty"`
	CompletedAt    int64  `json:"completed_at,omitempty"`
}

type DB struct {
	db *gorm.DB
}

func Open(dataPath string) (*DB, error) {
	conn, err := pluginsdk.OpenDatabase(dataPath, "scheduler.db", &Job{}, &ExecutionLog{}, &DispatchEntry{})
	if err != nil {
		return nil, err
	}
	return &DB{db: conn}, nil
}

func (d *DB) CreateJob(j *Job) error {
	if j.ID == "" {
		j.ID = uuid.New().String()
	}
	return d.db.Create(j).Error
}

func (d *DB) GetJob(id string) (*Job, error) {
	var j Job
	if err := d.db.First(&j, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &j, nil
}

func (d *DB) ListJobs() ([]Job, error) {
	var jobs []Job
	if err := d.db.Order("created_at DESC").Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

func (d *DB) ListEnabledJobs() ([]Job, error) {
	var jobs []Job
	if err := d.db.Where("enabled = ?", true).Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

func (d *DB) ListTimerJobs() ([]Job, error) {
	var jobs []Job
	if err := d.db.Where("enabled = ? AND (trigger_type = ? OR trigger_type = ?)", true, "timer", "").Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

func (d *DB) ListEventJobs() ([]Job, error) {
	var jobs []Job
	if err := d.db.Where("enabled = ? AND trigger_type = ?", true, "event").Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

func (d *DB) ListEventJobsByPattern(pattern string) ([]Job, error) {
	var jobs []Job
	if err := d.db.Where("enabled = ? AND trigger_type = ? AND event_pattern = ?", true, "event", pattern).Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

func (d *DB) UpdateJob(j *Job) error {
	return d.db.Save(j).Error
}

func (d *DB) DeleteJob(id string) error {
	return d.db.Delete(&Job{}, "id = ?", id).Error
}

func (d *DB) IncrementFired(id string, nextFire int64, disable bool) error {
	updates := map[string]interface{}{
		"fired_count": gorm.Expr("fired_count + 1"),
		"next_fire":   nextFire,
	}
	if disable {
		updates["enabled"] = false
	}
	return d.db.Model(&Job{}).Where("id = ?", id).Updates(updates).Error
}

func (d *DB) CreateLog(l *ExecutionLog) error {
	if l.ID == "" {
		l.ID = uuid.New().String()
	}
	return d.db.Create(l).Error
}

func (d *DB) ListLogs(limit int) ([]ExecutionLog, error) {
	var logs []ExecutionLog
	if err := d.db.Order("fired_at DESC").Limit(limit).Find(&logs).Error; err != nil {
		return nil, err
	}
	return logs, nil
}

// --- Dispatch Queue ---

func (d *DB) CreateDispatchEntry(e *DispatchEntry) error {
	if e.ID == "" {
		e.ID = uuid.New().String()
	}
	return d.db.Create(e).Error
}

func (d *DB) GetDispatchEntry(id string) (*DispatchEntry, error) {
	var e DispatchEntry
	if err := d.db.First(&e, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &e, nil
}

func (d *DB) ListDispatchEntries(status string, limit int) ([]DispatchEntry, error) {
	var entries []DispatchEntry
	q := d.db.Order("created_at DESC")
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	return entries, q.Find(&entries).Error
}

func (d *DB) CountInFlight(agentAlias string) (int64, error) {
	var count int64
	err := d.db.Model(&DispatchEntry{}).Where("status = ? AND agent_alias = ?", "dispatched", agentAlias).Count(&count).Error
	return count, err
}

func (d *DB) CountAllInFlight() (int64, error) {
	var count int64
	err := d.db.Model(&DispatchEntry{}).Where("status = ?", "dispatched").Count(&count).Error
	return count, err
}

func (d *DB) UpdateDispatchStatus(id, status string, fields map[string]interface{}) error {
	if fields == nil {
		fields = map[string]interface{}{}
	}
	fields["status"] = status
	return d.db.Model(&DispatchEntry{}).Where("id = ?", id).Updates(fields).Error
}

func (d *DB) ListPendingAgents() ([]string, error) {
	var agents []string
	err := d.db.Model(&DispatchEntry{}).Where("status = ?", "pending").Distinct().Pluck("agent_alias", &agents).Error
	return agents, err
}

func (d *DB) GetNextPending(agentAlias string) (*DispatchEntry, error) {
	var e DispatchEntry
	err := d.db.Where("status = ? AND agent_alias = ?", "pending", agentAlias).Order("created_at ASC").First(&e).Error
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (d *DB) HasPendingOrDispatched(cardID, agentAlias string) (bool, error) {
	var count int64
	err := d.db.Model(&DispatchEntry{}).Where("card_id = ? AND agent_alias = ? AND status IN ?", cardID, agentAlias, []string{"pending", "dispatched"}).Count(&count).Error
	return count > 0, err
}

func (d *DB) ResetStaleDispatched() (int64, error) {
	result := d.db.Model(&DispatchEntry{}).Where("status = ?", "dispatched").Update("status", "pending")
	return result.RowsAffected, result.Error
}
