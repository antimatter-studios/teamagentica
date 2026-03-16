package migrate

import (
	"fmt"
	"log"
	"sort"
	"time"

	"gorm.io/gorm"
)

// Record tracks which migrations have been applied.
type Record struct {
	ID        uint      `gorm:"primaryKey"`
	Name      string    `gorm:"uniqueIndex;not null"`
	AppliedAt time.Time `gorm:"not null"`
}

func (Record) TableName() string { return "migrations" }

// Func is a function that performs a single migration step.
type Func func(db *gorm.DB) error

type entry struct {
	name string
	fn   Func
}

var registry []entry

// Register adds a named migration to the global registry.
// Called from init() in individual migration files.
func Register(name string, fn Func) {
	registry = append(registry, entry{name: name, fn: fn})
}

// Run creates the migrations table if needed, then applies
// any registered migrations that haven't been run yet, in lexical order.
func Run(db *gorm.DB) error {
	// Ensure the migrations tracking table exists.
	if err := db.AutoMigrate(&Record{}); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	// Sort migrations by name so they run in deterministic order.
	sort.Slice(registry, func(i, j int) bool {
		return registry[i].name < registry[j].name
	})

	// Load already-applied migration names.
	var applied []Record
	if err := db.Find(&applied).Error; err != nil {
		return fmt.Errorf("failed to read migrations table: %w", err)
	}
	appliedSet := make(map[string]bool, len(applied))
	for _, m := range applied {
		appliedSet[m.Name] = true
	}

	// Apply pending migrations.
	pending := 0
	for _, m := range registry {
		if appliedSet[m.name] {
			log.Printf("migrations: skip %s (already applied)", m.name)
			continue
		}
		pending++

		log.Printf("migrations: applying %s ...", m.name)

		err := db.Transaction(func(tx *gorm.DB) error {
			if err := m.fn(tx); err != nil {
				return err
			}
			return tx.Create(&Record{
				Name:      m.name,
				AppliedAt: time.Now().UTC(),
			}).Error
		})
		if err != nil {
			return fmt.Errorf("migration %s failed: %w", m.name, err)
		}

		log.Printf("migrations: applied %s ✓", m.name)
	}

	log.Printf("migrations: %d registered, %d already applied, %d newly applied", len(registry), len(registry)-pending, pending)
	return nil
}
