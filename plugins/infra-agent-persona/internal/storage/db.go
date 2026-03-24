package storage

import (
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"gorm.io/gorm"
)

// Persona represents an agent personality with a system prompt and routing info.
type Persona struct {
	Alias        string         `json:"alias" gorm:"primaryKey"`
	SystemPrompt string         `json:"system_prompt" gorm:"not null"`
	Model        string         `json:"model" gorm:"default:''"`
	BackendAlias string         `json:"backend_alias" gorm:"default:''"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index"`
}

// DB wraps the GORM database for persona storage.
type DB struct {
	db *gorm.DB
}

// Open creates or opens the persona database.
func Open(dataPath string) (*DB, error) {
	db, err := pluginsdk.OpenDatabase(dataPath, "personas.db", &Persona{})
	if err != nil {
		return nil, err
	}
	return &DB{db: db}, nil
}

// List returns all personas.
func (d *DB) List() ([]Persona, error) {
	var personas []Persona
	if err := d.db.Order("alias").Find(&personas).Error; err != nil {
		return nil, err
	}
	return personas, nil
}

// Get returns a single persona by alias.
func (d *DB) Get(alias string) (*Persona, error) {
	var p Persona
	if err := d.db.First(&p, "alias = ?", alias).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

// Create inserts a new persona.
func (d *DB) Create(p *Persona) error {
	return d.db.Create(p).Error
}

// Update modifies an existing persona.
func (d *DB) Update(alias string, updates map[string]interface{}) error {
	updates["updated_at"] = time.Now()
	return d.db.Model(&Persona{}).Where("alias = ?", alias).Updates(updates).Error
}

// Delete removes a persona by alias.
func (d *DB) Delete(alias string) error {
	return d.db.Delete(&Persona{}, "alias = ?", alias).Error
}
