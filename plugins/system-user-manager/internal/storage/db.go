package storage

import (
	"errors"

	"gorm.io/gorm"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
)

// DB wraps the GORM connection for user management storage.
type DB struct {
	db *gorm.DB
}

// Open creates or opens the SQLite database at dataPath/users.db.
func Open(dataPath string) (*DB, error) {
	conn, err := pluginsdk.OpenDatabase(dataPath, "users.db", &Setting{}, &User{}, &ServiceToken{}, &AuditLog{}, &ExternalUser{})
	if err != nil {
		return nil, err
	}
	return &DB{db: conn}, nil
}

// Gorm returns the underlying GORM connection for direct queries.
func (d *DB) Gorm() *gorm.DB {
	return d.db
}

// --- Setting operations ---

// GetSetting retrieves a setting by key.
func (d *DB) GetSetting(key string) (string, error) {
	var s Setting
	err := d.db.First(&s, "key = ?", key).Error
	if err != nil {
		return "", err
	}
	return s.Value, nil
}

// SetSetting upserts a setting.
func (d *DB) SetSetting(key, value string) error {
	s := Setting{Key: key, Value: value}
	return d.db.Save(&s).Error
}

// --- User operations ---

// UserCount returns the total number of users.
func (d *DB) UserCount() (int64, error) {
	var count int64
	err := d.db.Model(&User{}).Count(&count).Error
	return count, err
}

// GetUserByEmail looks up a user by email.
func (d *DB) GetUserByEmail(email string) (*User, error) {
	var u User
	err := d.db.Where("email = ?", email).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &u, err
}

// GetUserByID looks up a user by ID.
func (d *DB) GetUserByID(id uint) (*User, error) {
	var u User
	err := d.db.First(&u, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &u, err
}

// CreateUser inserts a new user.
func (d *DB) CreateUser(u *User) error {
	existing, _ := d.GetUserByEmail(u.Email)
	if existing != nil {
		return ErrAlreadyExists
	}
	return d.db.Create(u).Error
}

// UpdateUser saves changes to an existing user.
func (d *DB) UpdateUser(u *User) error {
	return d.db.Save(u).Error
}

// ListUsers returns all users.
func (d *DB) ListUsers() ([]User, error) {
	var users []User
	err := d.db.Order("id ASC").Find(&users).Error
	return users, err
}

// DeleteUser removes a user by ID.
func (d *DB) DeleteUser(id uint) error {
	result := d.db.Delete(&User{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// --- ServiceToken operations ---

// CreateServiceToken inserts a new service token record.
func (d *DB) CreateServiceToken(st *ServiceToken) error {
	var existing ServiceToken
	if d.db.Where("name = ?", st.Name).First(&existing).Error == nil {
		return ErrAlreadyExists
	}
	return d.db.Create(st).Error
}

// ListServiceTokens returns all service tokens.
func (d *DB) ListServiceTokens() ([]ServiceToken, error) {
	var tokens []ServiceToken
	err := d.db.Order("created_at DESC").Find(&tokens).Error
	return tokens, err
}

// RevokeServiceToken marks a token as revoked.
func (d *DB) RevokeServiceToken(id uint) error {
	var token ServiceToken
	if err := d.db.First(&token, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	return d.db.Model(&token).Update("revoked", true).Error
}

// --- AuditLog operations ---

// CreateAuditLog inserts an audit log entry.
func (d *DB) CreateAuditLog(entry *AuditLog) error {
	return d.db.Create(entry).Error
}

// ListAuditLogs returns paginated audit logs with optional filters.
func (d *DB) ListAuditLogs(action, actorID string, limit, offset int) ([]AuditLog, int64, error) {
	query := d.db.Model(&AuditLog{}).Order("timestamp DESC")
	if action != "" {
		query = query.Where("action = ?", action)
	}
	if actorID != "" {
		query = query.Where("actor_id = ?", actorID)
	}

	var total int64
	query.Count(&total)

	var logs []AuditLog
	err := query.Limit(limit).Offset(offset).Find(&logs).Error
	return logs, total, err
}

// --- ExternalUser operations ---

// ListExternalUsers returns external user mappings, optionally filtered by source.
func (d *DB) ListExternalUsers(source string) ([]ExternalUser, error) {
	var mappings []ExternalUser
	q := d.db.Order("source, external_id")
	if source != "" {
		q = q.Where("source = ?", source)
	}
	err := q.Find(&mappings).Error
	return mappings, err
}

// CreateExternalUser inserts a new external user mapping.
func (d *DB) CreateExternalUser(eu *ExternalUser) error {
	return d.db.Create(eu).Error
}

// UpdateExternalUser saves changes to an external user mapping.
func (d *DB) UpdateExternalUser(eu *ExternalUser) error {
	return d.db.Save(eu).Error
}

// GetExternalUser retrieves an external user mapping by ID.
func (d *DB) GetExternalUser(id uint) (*ExternalUser, error) {
	var eu ExternalUser
	err := d.db.First(&eu, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &eu, err
}

// DeleteExternalUser removes an external user mapping.
func (d *DB) DeleteExternalUser(id uint) error {
	result := d.db.Delete(&ExternalUser{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
