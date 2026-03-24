package storage

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

// Alias type constants.
const (
	TypeAgent     = "agent"      // full persona: provider + model + system prompt
	TypeToolAgent = "tool_agent" // AI-powered tool plugin (e.g. nanobanana, seedance)
	TypeTool      = "tool"       // service plugin (e.g. storage-sss3)
)

// Alias defines an addressable name in the platform.
type Alias struct {
	Name         string         `json:"name" gorm:"primaryKey"`
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
	conn, err := pluginsdk.OpenDatabase(dataPath, "aliases.db", &Alias{})
	if err != nil {
		return nil, err
	}
	return &DB{db: conn}, nil
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
	err := d.db.First(&a, "name = ?", name).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &a, err
}

// Create inserts a new alias. Returns ErrAlreadyExists if name is taken.
func (d *DB) Create(a *Alias) error {
	var existing Alias
	if d.db.First(&existing, "name = ?", a.Name).Error == nil {
		return ErrAlreadyExists
	}
	return d.db.Create(a).Error
}

// Update saves all mutable fields for an existing alias.
func (d *DB) Update(a *Alias) error {
	return d.db.Save(a).Error
}

// Delete removes an alias by name.
func (d *DB) Delete(name string) error {
	result := d.db.Delete(&Alias{}, "name = ?", name)
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
