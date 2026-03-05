package migrate

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// resetRegistry clears the global registry so tests are isolated.
func resetRegistry() {
	registry = nil
}

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

func TestRun_AppliesMigrations(t *testing.T) {
	resetRegistry()
	db := openTestDB(t)

	applied := false
	Register("001_create_foo", func(tx *gorm.DB) error {
		applied = true
		return tx.Exec("CREATE TABLE foo (id INTEGER PRIMARY KEY)").Error
	})

	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !applied {
		t.Fatal("migration was not applied")
	}

	// Verify migrations table has a record.
	var records []Record
	db.Find(&records)
	if len(records) != 1 {
		t.Fatalf("expected 1 migration record, got %d", len(records))
	}
	if records[0].Name != "001_create_foo" {
		t.Fatalf("expected name 001_create_foo, got %s", records[0].Name)
	}
}

func TestRun_Idempotent(t *testing.T) {
	resetRegistry()
	db := openTestDB(t)

	callCount := 0
	Register("001_create_bar", func(tx *gorm.DB) error {
		callCount++
		return tx.Exec("CREATE TABLE IF NOT EXISTS bar (id INTEGER PRIMARY KEY)").Error
	})

	if err := Run(db); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if err := Run(db); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected migration called once, got %d", callCount)
	}
}

func TestRun_LexicalOrder(t *testing.T) {
	resetRegistry()
	db := openTestDB(t)

	var order []string

	// Register out of order on purpose.
	Register("003_third", func(tx *gorm.DB) error {
		order = append(order, "003")
		return nil
	})
	Register("001_first", func(tx *gorm.DB) error {
		order = append(order, "001")
		return nil
	})
	Register("002_second", func(tx *gorm.DB) error {
		order = append(order, "002")
		return nil
	})

	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	expected := []string{"001", "002", "003"}
	if len(order) != 3 {
		t.Fatalf("expected 3 migrations, got %d", len(order))
	}
	for i, v := range expected {
		if order[i] != v {
			t.Fatalf("position %d: expected %s, got %s", i, v, order[i])
		}
	}
}

func TestRun_MigrationError(t *testing.T) {
	resetRegistry()
	db := openTestDB(t)

	Register("001_bad", func(tx *gorm.DB) error {
		return tx.Exec("INVALID SQL STATEMENT").Error
	})

	err := Run(db)
	if err == nil {
		t.Fatal("expected error from bad migration")
	}

	// Verify the failed migration is NOT recorded.
	var count int64
	db.Model(&Record{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected 0 migration records after failure, got %d", count)
	}
}

func TestRun_EmptyRegistry(t *testing.T) {
	resetRegistry()
	db := openTestDB(t)

	if err := Run(db); err != nil {
		t.Fatalf("Run with empty registry: %v", err)
	}
}

func TestRun_PartialApply(t *testing.T) {
	resetRegistry()
	db := openTestDB(t)

	Register("001_first", func(tx *gorm.DB) error {
		return nil
	})

	if err := Run(db); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Add a second migration and run again.
	Register("002_second", func(tx *gorm.DB) error {
		return nil
	})

	if err := Run(db); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	var count int64
	db.Model(&Record{}).Count(&count)
	if count != 2 {
		t.Fatalf("expected 2 migration records, got %d", count)
	}
}
