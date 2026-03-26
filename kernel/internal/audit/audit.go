package audit

import (
	"fmt"
	"log"

	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/kernel/internal/database"
	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

// Logger records audit events to the database.
type Logger struct{}

// NewLogger creates a new audit Logger.
func NewLogger() *Logger {
	return &Logger{}
}

func (l *Logger) db() *gorm.DB { return database.Get() }

// Log records an audit event asynchronously.
func (l *Logger) Log(entry models.AuditLog) {
	go func() {
		if err := l.db().Create(&entry).Error; err != nil {
			log.Printf("audit: failed to write log entry: %v", err)
		}
	}()
}

// LogUserAction records an action performed by a user.
func (l *Logger) LogUserAction(userID uint, action, resource, detail, ip string, success bool) {
	l.Log(models.AuditLog{
		ActorType: "user",
		ActorID:   fmt.Sprintf("%d", userID),
		Action:    action,
		Resource:  resource,
		Detail:    detail,
		IP:        ip,
		Success:   success,
	})
}

// LogServiceAction records an action performed by a service.
func (l *Logger) LogServiceAction(serviceName, action, resource, detail, ip string, success bool) {
	l.Log(models.AuditLog{
		ActorType: "service",
		ActorID:   serviceName,
		Action:    action,
		Resource:  resource,
		Detail:    detail,
		IP:        ip,
		Success:   success,
	})
}

// LogSystemAction records an action performed by the kernel itself.
func (l *Logger) LogSystemAction(action, resource, detail string, success bool) {
	l.Log(models.AuditLog{
		ActorType: "system",
		ActorID:   "kernel",
		Action:    action,
		Resource:  resource,
		Detail:    detail,
		Success:   success,
	})
}
