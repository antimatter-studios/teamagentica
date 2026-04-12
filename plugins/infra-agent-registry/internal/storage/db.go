package storage

import (
	"log"
	"strings"
	"time"
	"unicode"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"gorm.io/gorm"
)

// Persona types.
const (
	TypeAgent     = "agent"
	TypeToolAgent = "tool_agent"
	TypeTool      = "tool"
)

// Sanitize normalises a name/alias/id: lowercase, trim, strip @,
// keep letters, digits, hyphens and underscores, collapse consecutive
// underscores/hyphens, and trim leading/trailing separators.
func Sanitize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "@", "")
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	result := b.String()
	for strings.Contains(result, "__") {
		result = strings.ReplaceAll(result, "__", "_")
	}
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	return strings.Trim(result, "_-")
}

// Persona represents an addressable agent, tool-agent, or tool with optional personality.
type Persona struct {
	Alias        string         `json:"alias" gorm:"primaryKey"`
	Type         string         `json:"type" gorm:"default:'agent'"`
	Plugin       string         `json:"plugin" gorm:"default:''"`
	SystemPrompt string         `json:"system_prompt" gorm:"default:''"`
	Model        string         `json:"model" gorm:"default:''"`
	IsDefault    *bool          `json:"is_default" gorm:"uniqueIndex:idx_persona_default"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index"`
}

// DB wraps the GORM database for persona storage.
type DB struct {
	db *gorm.DB
}

// Open creates or opens the persona database and runs migrations.
func Open(dataPath string) (*DB, error) {
	db, err := pluginsdk.OpenDatabase(dataPath, "personas.db", &Persona{})
	if err != nil {
		return nil, err
	}

	// One-time migration: populate new columns from old schema.
	// backend_alias → plugin, set default type.
	if db.Migrator().HasColumn(&Persona{}, "backend_alias") {
		if err := db.Exec("UPDATE personas SET plugin = backend_alias WHERE (plugin = '' OR plugin IS NULL) AND backend_alias != ''").Error; err != nil {
			log.Printf("persona: migration backend_alias→plugin: %v", err)
		}
	}
	if err := db.Exec("UPDATE personas SET type = 'agent' WHERE type = '' OR type IS NULL").Error; err != nil {
		log.Printf("persona: migration set default type: %v", err)
	}

	return &DB{db: db}, nil
}

// List returns all entries.
func (d *DB) List() ([]Persona, error) {
	var entries []Persona
	if err := d.db.Order("alias").Find(&entries).Error; err != nil {
		return nil, err
	}
	return entries, nil
}

// ListAgents returns entries that have a system_prompt or is_default set (agents with identity).
func (d *DB) ListAgents() ([]Persona, error) {
	var agents []Persona
	if err := d.db.Where("system_prompt != '' OR is_default = ?", true).Order("alias").Find(&agents).Error; err != nil {
		return nil, err
	}
	return agents, nil
}

// ListAliases returns entries that have no system_prompt and no is_default (bare routing entries).
func (d *DB) ListAliases() ([]Persona, error) {
	var aliases []Persona
	if err := d.db.Where("(system_prompt = '' OR system_prompt IS NULL) AND (is_default IS NULL OR is_default != ?)", true).Order("alias").Find(&aliases).Error; err != nil {
		return nil, err
	}
	return aliases, nil
}

// ListByType returns entries filtered by type.
func (d *DB) ListByType(t string) ([]Persona, error) {
	var entries []Persona
	if err := d.db.Where("type = ?", t).Order("alias").Find(&entries).Error; err != nil {
		return nil, err
	}
	return entries, nil
}

// Get returns a single persona by alias.
func (d *DB) Get(alias string) (*Persona, error) {
	var p Persona
	if err := d.db.First(&p, "alias = ?", Sanitize(alias)).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

// Create inserts a new persona.
func (d *DB) Create(p *Persona) error {
	p.Alias = Sanitize(p.Alias)
	if p.Type == "" {
		p.Type = TypeAgent
	}
	return d.db.Create(p).Error
}

// Update modifies an existing persona.
func (d *DB) Update(alias string, updates map[string]interface{}) error {
	if v, ok := updates["alias"]; ok {
		if s, ok := v.(string); ok {
			updates["alias"] = Sanitize(s)
		}
	}
	updates["updated_at"] = time.Now()
	return d.db.Model(&Persona{}).Where("alias = ?", Sanitize(alias)).Updates(updates).Error
}

// Rename changes a persona's alias (primary key).
func (d *DB) Rename(oldAlias, newAlias string) error {
	return d.db.Exec("UPDATE personas SET alias = ?, updated_at = ? WHERE alias = ?", Sanitize(newAlias), time.Now(), Sanitize(oldAlias)).Error
}

// Delete removes a persona by alias.
func (d *DB) Delete(alias string) error {
	return d.db.Delete(&Persona{}, "alias = ?", Sanitize(alias)).Error
}

// Upsert creates a persona if it doesn't exist, or updates type/plugin/model
// without touching system_prompt. Used for migration from alias-registry.
func (d *DB) Upsert(p *Persona) (created bool, err error) {
	existing, err := d.Get(p.Alias)
	if err != nil {
		// Not found — create.
		p.Alias = Sanitize(p.Alias)
		if p.Type == "" {
			p.Type = TypeAgent
		}
		return true, d.db.Create(p).Error
	}
	// Found — update routing fields only.
	updates := map[string]interface{}{
		"type":       p.Type,
		"plugin":     p.Plugin,
		"model":      p.Model,
		"updated_at": time.Now(),
	}
	// Don't overwrite an existing system_prompt with empty.
	if existing.SystemPrompt == "" && p.SystemPrompt != "" {
		updates["system_prompt"] = p.SystemPrompt
	}
	return false, d.db.Model(&Persona{}).Where("alias = ?", Sanitize(p.Alias)).Updates(updates).Error
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
	return d.db.Model(&Persona{}).Where("alias = ?", Sanitize(alias)).Update("is_default", &isTrue).Error
}

// ClearDefault removes the default flag from a persona.
func (d *DB) ClearDefault(alias string) error {
	return d.db.Model(&Persona{}).Where("alias = ?", Sanitize(alias)).Update("is_default", nil).Error
}
