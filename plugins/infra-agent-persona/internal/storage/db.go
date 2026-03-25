package storage

import (
	"errors"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"gorm.io/gorm"
)

// Role defines a persona role with optional cardinality constraints and a default system prompt.
type Role struct {
	ID           string    `json:"id" gorm:"primaryKey"`
	Label        string    `json:"label" gorm:"not null"`
	MaxCount     int       `json:"max_count" gorm:"default:0"`
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
	Role         string         `json:"role" gorm:"default:'worker';index:idx_persona_role"`
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

// RoleSeed defines a role to be seeded with its default system prompt.
type RoleSeed struct {
	ID           string
	Label        string
	MaxCount     int
	SystemPrompt string
}

// SeedRoles creates default roles if they don't already exist.
func (d *DB) SeedRoles(seeds []RoleSeed) error {
	for _, s := range seeds {
		var existing Role
		if err := d.db.Where("id = ?", s.ID).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				r := Role{
					ID:           s.ID,
					Label:        s.Label,
					MaxCount:     s.MaxCount,
					SystemPrompt: s.SystemPrompt,
				}
				if err := d.db.Create(&r).Error; err != nil {
					return err
				}
			} else {
				return err
			}
		}
	}
	return nil
}

// ResetRolePrompts overwrites role system_prompts with the given defaults.
func (d *DB) ResetRolePrompts(seeds []RoleSeed) error {
	for _, s := range seeds {
		if s.SystemPrompt == "" {
			continue
		}
		d.db.Model(&Role{}).Where("id = ?", s.ID).Update("system_prompt", s.SystemPrompt)
	}
	return nil
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
	if p.Role == "" {
		p.Role = "worker"
	}
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
	if err := d.db.First(&r, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &r, nil
}

// CreateRole inserts a new role.
func (d *DB) CreateRole(r *Role) error {
	return d.db.Create(r).Error
}

// UpdateRole modifies an existing role.
func (d *DB) UpdateRole(id string, updates map[string]interface{}) (*Role, error) {
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

// DeleteRole removes a role by ID. The "worker" role cannot be deleted.
func (d *DB) DeleteRole(id string) error {
	if id == "worker" {
		return errors.New("cannot delete the worker role")
	}
	return d.db.Delete(&Role{}, "id = ?", id).Error
}

// GetRolePrompt returns the system_prompt for a given role, or empty string if not found.
func (d *DB) GetRolePrompt(roleID string) string {
	var r Role
	if err := d.db.Select("system_prompt").First(&r, "id = ?", roleID).Error; err != nil {
		return ""
	}
	return r.SystemPrompt
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

// AssignRole assigns a role to a persona, trimming excess holders if MaxCount > 0.
func (d *DB) AssignRole(alias, role string) error {
	// Verify persona exists.
	if _, err := d.Get(alias); err != nil {
		return errors.New("persona not found")
	}

	// Verify role exists.
	r, err := d.GetRole(role)
	if err != nil {
		return errors.New("role not found")
	}

	// Enforce MaxCount: bump oldest holders back to "worker" if needed.
	if r.MaxCount > 0 {
		var holders []Persona
		d.db.Where("role = ? AND alias != ?", role, alias).Order("created_at").Find(&holders)
		excess := len(holders) - r.MaxCount + 1 // +1 because we're about to assign one more
		if excess > 0 {
			for i := 0; i < excess && i < len(holders); i++ {
				d.db.Model(&Persona{}).Where("alias = ?", holders[i].Alias).Update("role", "worker")
			}
		}
	}

	return d.db.Model(&Persona{}).Where("alias = ?", alias).Update("role", role).Error
}
