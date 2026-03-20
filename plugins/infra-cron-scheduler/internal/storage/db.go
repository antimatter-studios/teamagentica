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
	Schedule     string `json:"schedule"`       // "5m" or "*/5 * * * *"
	ScheduleType string `json:"schedule_type"`  // "cron" | "interval"
	ActionType   string `json:"action_type"`    // "log" for now
	ActionConfig string `json:"action_config"`  // JSON blob for future use
	Enabled      bool   `json:"enabled"`
	FiredCount   int    `json:"fired_count"`
	NextFire     int64  `json:"next_fire"`      // unix ms
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

type DB struct {
	db *gorm.DB
}

func Open(dataPath string) (*DB, error) {
	conn, err := pluginsdk.OpenDatabase(dataPath, "scheduler.db", &Job{}, &ExecutionLog{})
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
