package audit

import (
	"fmt"
	"log"

	"github.com/antimatter-studios/teamagentica/plugins/system-user-manager/internal/storage"
)

// Logger records audit events to the database.
type Logger struct {
	db *storage.DB
}

// NewLogger creates a new audit Logger.
func NewLogger(db *storage.DB) *Logger {
	return &Logger{db: db}
}

// Log records an audit event asynchronously.
func (l *Logger) Log(entry storage.AuditLog) {
	go func() {
		if err := l.db.CreateAuditLog(&entry); err != nil {
			log.Printf("audit: failed to write log entry: %v", err)
		}
	}()
}

// LogUserAction records an action performed by a user.
func (l *Logger) LogUserAction(userID uint, action, resource, detail, ip string, success bool) {
	l.Log(storage.AuditLog{
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
	l.Log(storage.AuditLog{
		ActorType: "service",
		ActorID:   serviceName,
		Action:    action,
		Resource:  resource,
		Detail:    detail,
		IP:        ip,
		Success:   success,
	})
}
