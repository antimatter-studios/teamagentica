package database

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-authz/internal/models"
	"gorm.io/gorm"
)

// DB wraps the GORM connection for authz storage.
type DB struct {
	db *gorm.DB
}

// Open creates or opens the SQLite database at dataPath/authz.db.
func Open(dataPath string) (*DB, error) {
	conn, err := pluginsdk.OpenDatabase(dataPath, "authz.db",
		&models.PluginIdentity{},
		&models.Role{},
		&models.PrincipalRole{},
		&models.ScopeGrant{},
		&models.AuditEvent{},
		&models.ElevationGrant{},
	)
	if err != nil {
		return nil, err
	}
	return &DB{db: conn}, nil
}

// NewFromGorm wraps an existing GORM connection (used in tests).
func NewFromGorm(conn *gorm.DB) *DB {
	return &DB{db: conn}
}

// Gorm returns the underlying GORM connection.
func (d *DB) Gorm() *gorm.DB { return d.db }

// --- PluginIdentity ---

func (d *DB) UpsertIdentity(id *models.PluginIdentity) error {
	return d.db.Save(id).Error
}

func (d *DB) GetIdentityByPlugin(pluginID string) (*models.PluginIdentity, error) {
	var id models.PluginIdentity
	err := d.db.Where("plugin_id = ?", pluginID).First(&id).Error
	return &id, err
}

func (d *DB) GetIdentityByPrincipal(principal string) (*models.PluginIdentity, error) {
	var id models.PluginIdentity
	err := d.db.Where("principal = ?", principal).First(&id).Error
	return &id, err
}

// --- Roles ---

func (d *DB) CreateRole(role *models.Role) error {
	return d.db.Create(role).Error
}

func (d *DB) UpdateRole(role *models.Role) error {
	return d.db.Save(role).Error
}

func (d *DB) GetRole(id string) (*models.Role, error) {
	var role models.Role
	err := d.db.Where("id = ?", id).First(&role).Error
	return &role, err
}

func (d *DB) ListRoles() ([]models.Role, error) {
	var roles []models.Role
	err := d.db.Find(&roles).Error
	return roles, err
}

func (d *DB) DeleteRole(id string) error {
	return d.db.Where("id = ?", id).Delete(&models.Role{}).Error
}

// --- PrincipalRole ---

func (d *DB) AssignRole(pr *models.PrincipalRole) error {
	return d.db.Create(pr).Error
}

func (d *DB) GetPrincipalRoles(principal string) ([]models.PrincipalRole, error) {
	var prs []models.PrincipalRole
	err := d.db.Where("principal = ?", principal).Find(&prs).Error
	return prs, err
}

func (d *DB) RevokeRole(principal, roleID string) error {
	return d.db.Where("principal = ? AND role_id = ?", principal, roleID).Delete(&models.PrincipalRole{}).Error
}

// --- ScopeGrant ---

func (d *DB) CreateGrant(grant *models.ScopeGrant) error {
	return d.db.Create(grant).Error
}

func (d *DB) GetPrincipalGrants(principal string) ([]models.ScopeGrant, error) {
	var grants []models.ScopeGrant
	err := d.db.Where("principal = ?", principal).Find(&grants).Error
	return grants, err
}

func (d *DB) RevokeGrant(id string) error {
	return d.db.Where("id = ?", id).Delete(&models.ScopeGrant{}).Error
}

// --- AuditEvent ---

func (d *DB) InsertAudit(event *models.AuditEvent) error {
	return d.db.Create(event).Error
}

func (d *DB) ListAuditEvents(principal, projectID string, limit int) ([]models.AuditEvent, error) {
	q := d.db.Order("created_at DESC")
	if principal != "" {
		q = q.Where("principal = ?", principal)
	}
	if projectID != "" {
		q = q.Where("project_id = ?", projectID)
	}
	if limit > 0 {
		q = q.Limit(limit)
	} else {
		q = q.Limit(100)
	}
	var events []models.AuditEvent
	err := q.Find(&events).Error
	return events, err
}

func (d *DB) GetLastAuditHash() (string, error) {
	var event models.AuditEvent
	err := d.db.Order("created_at DESC").First(&event).Error
	if err != nil {
		return "", nil // no events yet
	}
	return event.Hash, nil
}

// --- ElevationGrant ---

func (d *DB) CreateElevationGrant(grant *models.ElevationGrant) error {
	return d.db.Create(grant).Error
}

func (d *DB) GetElevationGrant(id string) (*models.ElevationGrant, error) {
	var grant models.ElevationGrant
	err := d.db.Where("id = ?", id).First(&grant).Error
	return &grant, err
}

func (d *DB) ApproveElevationGrant(id, approvedBy string, expiresAt int64) error {
	return d.db.Model(&models.ElevationGrant{}).
		Where("id = ? AND status = ?", id, "pending").
		Updates(map[string]interface{}{
			"status":      "approved",
			"approved_by": approvedBy,
			"expires_at":  expiresAt,
		}).Error
}

func (d *DB) RevokeElevationGrant(id string) error {
	return d.db.Model(&models.ElevationGrant{}).
		Where("id = ? AND status = ?", id, "approved").
		Update("status", "revoked").Error
}

func (d *DB) ConsumeElevationGrant(id string) error {
	return d.db.Model(&models.ElevationGrant{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"consumed": true,
			"status":   "consumed",
		}).Error
}

func (d *DB) FindActiveGrant(principal, scope, projectID string) (*models.ElevationGrant, error) {
	var grant models.ElevationGrant
	q := d.db.Where(
		"principal = ? AND scope = ? AND status = ? AND consumed = ? AND (expires_at = 0 OR expires_at > ?)",
		principal, scope, "approved", false, currentUnix(),
	)
	if projectID != "" {
		q = q.Where("project_id = ?", projectID)
	}
	err := q.First(&grant).Error
	return &grant, err
}

func (d *DB) ExpireOldGrants() (int64, error) {
	now := currentUnix()

	// Expire approved grants past their expiry time
	r1 := d.db.Model(&models.ElevationGrant{}).
		Where("status = ? AND expires_at > 0 AND expires_at < ?", "approved", now).
		Update("status", "expired")

	// Expire pending requests past their request TTL
	r2 := d.db.Model(&models.ElevationGrant{}).
		Where("status = ? AND request_ttl > 0 AND (created_at + request_ttl) < ?", "pending", now).
		Update("status", "expired")

	total := r1.RowsAffected + r2.RowsAffected
	if r1.Error != nil {
		return total, r1.Error
	}
	return total, r2.Error
}

func (d *DB) ListElevationGrants(principal, status string) ([]models.ElevationGrant, error) {
	var grants []models.ElevationGrant
	q := d.db.Order("created_at DESC")
	if principal != "" {
		q = q.Where("principal = ?", principal)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	err := q.Find(&grants).Error
	return grants, err
}

// UpsertRole creates a role if it doesn't exist by name, or updates scopes/description if it does.
func (d *DB) UpsertRole(role *models.Role) error {
	var existing models.Role
	err := d.db.Where("name = ?", role.Name).First(&existing).Error
	if err != nil {
		return d.db.Create(role).Error
	}
	return d.db.Model(&existing).Updates(map[string]interface{}{
		"scopes":      role.Scopes,
		"description": role.Description,
		"updated_at":  role.UpdatedAt,
	}).Error
}

// SeedDefaultRoles creates or updates the built-in RBAC roles.
func (d *DB) SeedDefaultRoles() error {
	type roleDef struct {
		Name        string
		Description string
		Scopes      []string
	}

	defaults := []roleDef{
		{
			Name:        "role:agent",
			Description: "Default role for agent plugins",
			Scopes:      []string{"memory.*", "relay.send", "persona.read", "workspace.create", "cost.read"},
		},
		{
			Name:        "role:infra",
			Description: "Default role for infrastructure plugins",
			Scopes:      []string{"memory.*", "storage.*", "container.build", "deploy.candidate", "persona.*", "relay.send", "cost.read"},
		},
		{
			Name:        "role:admin",
			Description: "Full access to all scopes",
			Scopes:      []string{"*"},
		},
	}

	now := time.Now().Unix()
	for _, rd := range defaults {
		scopesJSON, _ := json.Marshal(rd.Scopes)
		role := &models.Role{
			ID:          rd.Name,
			Name:        rd.Name,
			Description: rd.Description,
			Scopes:      string(scopesJSON),
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := d.UpsertRole(role); err != nil {
			return err
		}
	}
	return nil
}

// --- Audit query/stats/verify ---

// AuditFilter holds query parameters for filtered audit listing.
type AuditFilter struct {
	Principal string
	ProjectID string
	Decision  string
	Scope     string
	Since     int64
	Limit     int
}

// ListAuditEventsFiltered returns audit events matching the filter.
func (d *DB) ListAuditEventsFiltered(f AuditFilter) ([]models.AuditEvent, error) {
	q := d.db.Order("created_at DESC")
	if f.Principal != "" {
		q = q.Where("principal = ?", f.Principal)
	}
	if f.ProjectID != "" {
		q = q.Where("project_id = ?", f.ProjectID)
	}
	if f.Decision != "" {
		q = q.Where("decision = ?", f.Decision)
	}
	if f.Scope != "" {
		q = q.Where("scope = ?", f.Scope)
	}
	if f.Since > 0 {
		q = q.Where("created_at >= ?", f.Since)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	q = q.Limit(limit)

	var events []models.AuditEvent
	err := q.Find(&events).Error
	return events, err
}

// AuditStatsResult holds aggregate audit statistics.
type AuditStatsResult struct {
	Total            int64            `json:"total"`
	AllowCount       int64            `json:"allow_count"`
	DenyCount        int64            `json:"deny_count"`
	EventsLastHour   int64            `json:"events_last_hour"`
	EventsLastDay    int64            `json:"events_last_day"`
	TopDeniedPrincipals []DeniedCount `json:"top_denied_principals"`
	TopDeniedScopes     []DeniedCount `json:"top_denied_scopes"`
}

// DeniedCount is a principal or scope with its deny count.
type DeniedCount struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// AuditStats returns aggregate audit statistics.
func (d *DB) AuditStats() (*AuditStatsResult, error) {
	var stats AuditStatsResult

	d.db.Model(&models.AuditEvent{}).Count(&stats.Total)
	d.db.Model(&models.AuditEvent{}).Where("decision = ?", "allow").Count(&stats.AllowCount)
	d.db.Model(&models.AuditEvent{}).Where("decision = ?", "deny").Count(&stats.DenyCount)

	now := time.Now().Unix()
	d.db.Model(&models.AuditEvent{}).Where("created_at >= ?", now-3600).Count(&stats.EventsLastHour)
	d.db.Model(&models.AuditEvent{}).Where("created_at >= ?", now-86400).Count(&stats.EventsLastDay)

	var topPrincipals []struct {
		Principal string
		Cnt       int64
	}
	d.db.Model(&models.AuditEvent{}).
		Select("principal, count(*) as cnt").
		Where("decision = ?", "deny").
		Group("principal").
		Order("cnt DESC").
		Limit(10).
		Find(&topPrincipals)

	for _, p := range topPrincipals {
		stats.TopDeniedPrincipals = append(stats.TopDeniedPrincipals, DeniedCount{Name: p.Principal, Count: p.Cnt})
	}

	var topScopes []struct {
		Scope string
		Cnt   int64
	}
	d.db.Model(&models.AuditEvent{}).
		Select("scope, count(*) as cnt").
		Where("decision = ?", "deny").
		Group("scope").
		Order("cnt DESC").
		Limit(10).
		Find(&topScopes)

	for _, s := range topScopes {
		stats.TopDeniedScopes = append(stats.TopDeniedScopes, DeniedCount{Name: s.Scope, Count: s.Cnt})
	}

	return &stats, nil
}

// AuditVerifyResult holds the result of hash chain verification.
type AuditVerifyResult struct {
	TotalChecked   int    `json:"total_checked"`
	ValidCount     int    `json:"valid_count"`
	FirstInvalidID string `json:"first_invalid_id,omitempty"`
	Valid          bool   `json:"valid"`
}

// AuditVerifyChain walks the hash chain and verifies integrity.
func (d *DB) AuditVerifyChain() (*AuditVerifyResult, error) {
	result := &AuditVerifyResult{Valid: true}
	batchSize := 1000
	offset := 0

	prevHash := ""
	for {
		var events []models.AuditEvent
		err := d.db.Order("created_at ASC, id ASC").Offset(offset).Limit(batchSize).Find(&events).Error
		if err != nil {
			return nil, err
		}
		if len(events) == 0 {
			break
		}

		for _, evt := range events {
			result.TotalChecked++
			data := fmt.Sprintf("%s|%s|%s|%s|%s|%d", prevHash, evt.Principal, evt.Scope, evt.Resource, evt.Decision, evt.CreatedAt)
			raw := sha256.Sum256([]byte(data))
			expected := base64.RawURLEncoding.EncodeToString(raw[:])
			if evt.Hash != expected || evt.PrevHash != prevHash {
				result.Valid = false
				result.FirstInvalidID = evt.ID
				return result, nil
			}
			result.ValidCount++
			prevHash = evt.Hash
		}

		offset += batchSize
	}

	return result, nil
}

func currentUnix() int64 {
	return time.Now().Unix()
}
