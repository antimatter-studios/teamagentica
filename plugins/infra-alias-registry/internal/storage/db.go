package storage

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

// sanitize normalises a name/alias/id: lowercase, trim, strip @,
// replace any non-alphabetical character with underscore, collapse
// consecutive underscores, and trim leading/trailing underscores.
func sanitize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "@", "")
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || r == '-' || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	result := b.String()
	for strings.Contains(result, "__") {
		result = strings.ReplaceAll(result, "__", "_")
	}
	return strings.Trim(result, "_")
}

// Alias type constants.
const (
	TypeAgent     = "agent"      // full persona: provider + model + system prompt
	TypeToolAgent = "tool_agent" // AI-powered tool plugin (e.g. nanobanana, seedance)
	TypeTool      = "tool"       // service plugin (e.g. storage-sss3)
)

// Alias defines an addressable name in the platform.
type Alias struct {
	ID           uint           `json:"-" gorm:"primaryKey;autoIncrement"`
	Name         string         `json:"name" gorm:"not null"`
	Type         string         `json:"type" gorm:"default:'tool'"`
	Plugin       string         `json:"plugin" gorm:"not null"`
	Provider     string         `json:"provider,omitempty" gorm:"default:''"`
	Model        string         `json:"model,omitempty" gorm:"default:''"`
	SystemPrompt string         `json:"system_prompt,omitempty" gorm:"default:''"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index"`
}

// DB wraps the GORM connection for alias storage.
type DB struct {
	db *gorm.DB
}

// Open creates or opens the SQLite database at dataPath/aliases.db.
func Open(dataPath string) (*DB, error) {
	conn, err := pluginsdk.OpenDatabase(dataPath, "aliases.db")
	if err != nil {
		return nil, err
	}

	// Migrate from old schema (name as primary key) to new (id + partial unique index).
	if err := migrateSchema(conn); err != nil {
		return nil, err
	}

	return &DB{db: conn}, nil
}

// migrateSchema handles the one-time migration from name-as-PK to id-as-PK
// and creates a partial unique index on (name) WHERE deleted_at IS NULL.
func migrateSchema(db *gorm.DB) error {
	// Check if the old schema is in use (name is the PK, no id column).
	var hasID int
	db.Raw("SELECT COUNT(*) FROM pragma_table_info('aliases') WHERE name='id'").Scan(&hasID)

	if hasID == 0 {
		// Old schema: name is PK. Rebuild the table with id as PK.
		stmts := []string{
			`ALTER TABLE aliases RENAME TO _aliases_old`,
			`CREATE TABLE aliases (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT NOT NULL,
				type TEXT DEFAULT 'tool',
				plugin TEXT NOT NULL,
				provider TEXT DEFAULT '',
				model TEXT DEFAULT '',
				system_prompt TEXT DEFAULT '',
				created_at DATETIME,
				updated_at DATETIME,
				deleted_at DATETIME
			)`,
			`INSERT INTO aliases (name, type, plugin, provider, model, system_prompt, created_at, updated_at, deleted_at)
				SELECT name, type, plugin, provider, model, system_prompt, created_at, updated_at, deleted_at FROM _aliases_old`,
			`DROP TABLE _aliases_old`,
			`CREATE INDEX idx_aliases_deleted_at ON aliases(deleted_at)`,
			`CREATE UNIQUE INDEX idx_aliases_name_active ON aliases(name) WHERE deleted_at IS NULL`,
		}
		for _, s := range stmts {
			if err := db.Exec(s).Error; err != nil {
				return fmt.Errorf("migration failed: %w", err)
			}
		}
		return nil
	}

	// New schema already exists — just ensure the partial unique index is present.
	db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_aliases_name_active ON aliases(name) WHERE deleted_at IS NULL`)

	// Run AutoMigrate for any future column additions.
	return db.AutoMigrate(&Alias{})
}

// List returns all aliases ordered by type then name.
func (d *DB) List() ([]Alias, error) {
	var aliases []Alias
	err := d.db.Order("type ASC, name ASC").Find(&aliases).Error
	return aliases, err
}

// ListByType returns aliases of a specific type.
func (d *DB) ListByType(aliasType string) ([]Alias, error) {
	var aliases []Alias
	err := d.db.Where("type = ?", aliasType).Order("name ASC").Find(&aliases).Error
	return aliases, err
}

// Get returns a single alias by name, or ErrNotFound.
func (d *DB) Get(name string) (*Alias, error) {
	var a Alias
	err := d.db.First(&a, "name = ?", sanitize(name)).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &a, err
}

// Create inserts a new alias. Returns ErrAlreadyExists if name is taken.
func (d *DB) Create(a *Alias) error {
	a.Name = sanitize(a.Name)
	var existing Alias
	if d.db.First(&existing, "name = ?", a.Name).Error == nil {
		return ErrAlreadyExists
	}
	return d.db.Create(a).Error
}

// Update saves all mutable fields for an existing alias.
func (d *DB) Update(a *Alias) error {
	a.Name = sanitize(a.Name)
	return d.db.Save(a).Error
}

// Rename changes an alias's name.
func (d *DB) Rename(oldName, newName string) error {
	return d.db.Exec("UPDATE aliases SET name = ?, updated_at = ? WHERE name = ?", sanitize(newName), time.Now(), sanitize(oldName)).Error
}

// Delete removes an alias by name.
func (d *DB) Delete(name string) error {
	result := d.db.Delete(&Alias{}, "name = ?", sanitize(name))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

var (
	ErrNotFound      = errors.New("alias not found")
	ErrAlreadyExists = errors.New("alias already exists")
)
