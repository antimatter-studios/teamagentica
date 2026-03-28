package storage

import (
	"errors"
	"strings"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"gorm.io/gorm"
)

// sanitize normalises a name/alias/id: lowercase, trimmed, all @ removed.
func sanitize(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "@", "")
}

// Role defines a persona role with a default system prompt.
type Role struct {
	ID           string    `json:"id" gorm:"primaryKey"`
	Label        string    `json:"label" gorm:"not null"`
	SystemPrompt string    `json:"system_prompt" gorm:"default:''"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Persona represents an agent personality with a system prompt and routing info.
type Persona struct {
	Alias        string         `json:"alias" gorm:"primaryKey"`
	SystemPrompt string         `json:"system_prompt" gorm:"not null"`
	Model        string         `json:"model" gorm:"default:''"`
	BackendAlias string         `json:"backend_alias" gorm:"default:''"`
	Role         string         `json:"role" gorm:"default:'';index:idx_persona_role"`
	IsDefault    *bool          `json:"is_default" gorm:"uniqueIndex:idx_persona_default"`
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
	db, err := pluginsdk.OpenDatabase(dataPath, "personas.db", &Role{}, &Persona{})
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
	if err := d.db.First(&p, "alias = ?", sanitize(alias)).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

// Create inserts a new persona.
func (d *DB) Create(p *Persona) error {
	p.Alias = sanitize(p.Alias)
	return d.db.Create(p).Error
}

// Update modifies an existing persona.
func (d *DB) Update(alias string, updates map[string]interface{}) error {
	if v, ok := updates["alias"]; ok {
		if s, ok := v.(string); ok {
			updates["alias"] = sanitize(s)
		}
	}
	updates["updated_at"] = time.Now()
	return d.db.Model(&Persona{}).Where("alias = ?", sanitize(alias)).Updates(updates).Error
}

// Rename changes a persona's alias (primary key).
func (d *DB) Rename(oldAlias, newAlias string) error {
	return d.db.Exec("UPDATE personas SET alias = ?, updated_at = ? WHERE alias = ?", sanitize(newAlias), time.Now(), sanitize(oldAlias)).Error
}

// Delete removes a persona by alias.
func (d *DB) Delete(alias string) error {
	return d.db.Delete(&Persona{}, "alias = ?", sanitize(alias)).Error
}

// ListRoles returns all roles.
func (d *DB) ListRoles() ([]Role, error) {
	var roles []Role
	if err := d.db.Order("id").Find(&roles).Error; err != nil {
		return nil, err
	}
	return roles, nil
}

// GetRole returns a single role by ID.
func (d *DB) GetRole(id string) (*Role, error) {
	var r Role
	if err := d.db.First(&r, "id = ?", sanitize(id)).Error; err != nil {
		return nil, err
	}
	return &r, nil
}

// CreateRole inserts a new role.
func (d *DB) CreateRole(r *Role) error {
	r.ID = sanitize(r.ID)
	return d.db.Create(r).Error
}

// UpdateRole modifies an existing role.
func (d *DB) UpdateRole(id string, updates map[string]interface{}) (*Role, error) {
	id = sanitize(id)
	updates["updated_at"] = time.Now()
	if err := d.db.Model(&Role{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return nil, err
	}
	var r Role
	if err := d.db.First(&r, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &r, nil
}

// DeleteRole removes a role by ID.
func (d *DB) DeleteRole(id string) error {
	return d.db.Delete(&Role{}, "id = ?", sanitize(id)).Error
}

// GetRolePrompt returns the system_prompt for a given role, or empty string if not found.
func (d *DB) GetRolePrompt(roleID string) string {
	var r Role
	if err := d.db.Select("system_prompt").First(&r, "id = ?", sanitize(roleID)).Error; err != nil {
		return ""
	}
	return r.SystemPrompt
}

// GetDefault returns the persona marked as the default, or nil if none set.
func (d *DB) GetDefault() (*Persona, error) {
	var p Persona
	if err := d.db.Where("is_default = ?", true).First(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

// SetDefault marks the given persona as the default, clearing any previous default.
func (d *DB) SetDefault(alias string) error {
	d.db.Model(&Persona{}).Where("is_default = ?", true).Update("is_default", nil)
	isTrue := true
	return d.db.Model(&Persona{}).Where("alias = ?", sanitize(alias)).Update("is_default", &isTrue).Error
}

// ClearDefault removes the default flag from a persona.
func (d *DB) ClearDefault(alias string) error {
	return d.db.Model(&Persona{}).Where("alias = ?", sanitize(alias)).Update("is_default", nil).Error
}

// GetPersonasByRole returns all personas with the given role.
func (d *DB) GetPersonasByRole(role string) ([]Persona, error) {
	var personas []Persona
	if err := d.db.Where("role = ?", role).Order("created_at").Find(&personas).Error; err != nil {
		return nil, err
	}
	return personas, nil
}

// GetPersonaByRole returns the first persona with the given role.
func (d *DB) GetPersonaByRole(role string) (*Persona, error) {
	var p Persona
	if err := d.db.Where("role = ?", role).Order("created_at").First(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

// AssignRole assigns a role to a persona.
func (d *DB) AssignRole(alias, role string) error {
	alias = sanitize(alias)
	role = sanitize(role)
	if _, err := d.Get(alias); err != nil {
		return errors.New("persona not found")
	}
	if _, err := d.GetRole(role); err != nil {
		return errors.New("role not found")
	}
	return d.db.Model(&Persona{}).Where("alias = ?", alias).Update("role", role).Error
}
