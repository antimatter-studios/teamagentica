package database

import (
	"fmt"
	"testing"

	"github.com/antimatter-studios/teamagentica/plugins/infra-authz/internal/models"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) *DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	conn.AutoMigrate(
		&models.PluginIdentity{},
		&models.Role{},
		&models.PrincipalRole{},
		&models.ScopeGrant{},
		&models.AuditEvent{},
		&models.ElevationGrant{},
	)
	return &DB{db: conn}
}

func TestAuditHashChainValid(t *testing.T) {
	db := setupTestDB(t)

	prevHash := ""
	for i := 0; i < 10; i++ {
		ts := int64(1000 + i)
		principal := fmt.Sprintf("agent:p1:inst%d", i)
		scope := "memory.read"
		resource := "/data"
		decision := "allow"

		hash := models.AuditHash(prevHash, principal, scope, resource, decision, ts)
		evt := &models.AuditEvent{
			ID:        uuid.New().String(),
			Principal: principal,
			ProjectID: "p1",
			Scope:     scope,
			Resource:  resource,
			Decision:  decision,
			Reason:    "test",
			PrevHash:  prevHash,
			Hash:      hash,
			RequestID: uuid.New().String(),
			CreatedAt: ts,
		}
		if err := db.InsertAudit(evt); err != nil {
			t.Fatalf("insert audit %d: %v", i, err)
		}
		prevHash = hash
	}

	result, err := db.AuditVerifyChain()
	if err != nil {
		t.Fatalf("verify chain: %v", err)
	}
	if !result.Valid {
		t.Errorf("chain should be valid, first invalid: %s", result.FirstInvalidID)
	}
	if result.TotalChecked != 10 {
		t.Errorf("total checked = %d, want 10", result.TotalChecked)
	}
	if result.ValidCount != 10 {
		t.Errorf("valid count = %d, want 10", result.ValidCount)
	}
}

func TestAuditHashChainTamperDetected(t *testing.T) {
	db := setupTestDB(t)

	var eventIDs []string
	prevHash := ""
	for i := 0; i < 10; i++ {
		ts := int64(1000 + i)
		principal := fmt.Sprintf("agent:p1:inst%d", i)
		scope := "memory.read"
		resource := "/data"
		decision := "allow"

		hash := models.AuditHash(prevHash, principal, scope, resource, decision, ts)
		id := uuid.New().String()
		evt := &models.AuditEvent{
			ID:        id,
			Principal: principal,
			ProjectID: "p1",
			Scope:     scope,
			Resource:  resource,
			Decision:  decision,
			Reason:    "test",
			PrevHash:  prevHash,
			Hash:      hash,
			RequestID: uuid.New().String(),
			CreatedAt: ts,
		}
		if err := db.InsertAudit(evt); err != nil {
			t.Fatalf("insert audit %d: %v", i, err)
		}
		eventIDs = append(eventIDs, id)
		prevHash = hash
	}

	// Tamper with event 5's hash
	db.db.Model(&models.AuditEvent{}).Where("id = ?", eventIDs[5]).Update("hash", "TAMPERED")

	result, err := db.AuditVerifyChain()
	if err != nil {
		t.Fatalf("verify chain: %v", err)
	}
	if result.Valid {
		t.Error("chain should be invalid after tampering")
	}
	if result.FirstInvalidID != eventIDs[5] {
		t.Errorf("first invalid = %s, want %s", result.FirstInvalidID, eventIDs[5])
	}
}
