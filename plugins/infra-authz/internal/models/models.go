package models

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// AuditHash computes the hash for an audit event, creating a hash chain.
func AuditHash(prevHash, principal, scope, resource, decision string, ts int64) string {
	data := fmt.Sprintf("%s|%s|%s|%s|%s|%d", prevHash, principal, scope, resource, decision, ts)
	hash := sha256.Sum256([]byte(data))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

// PluginIdentity is a principal identity registered by the kernel at container start.
type PluginIdentity struct {
	ID        string `json:"id" gorm:"primaryKey"`
	PluginID  string `json:"plugin_id" gorm:"uniqueIndex"`
	Principal string `json:"principal"` // agent:{project_id}:{instance_id}
	ProjectID string `json:"project_id" gorm:"index"`
	AgentType string `json:"agent_type"`
	Scopes    string `json:"scopes"` // JSON array of granted scopes
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// Role is an RBAC role definition.
type Role struct {
	ID          string `json:"id" gorm:"primaryKey"`
	Name        string `json:"name" gorm:"uniqueIndex"`
	Description string `json:"description"`
	Scopes      string `json:"scopes"` // JSON array of scope patterns (wildcards OK)
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// PrincipalRole is a role assignment to a principal.
type PrincipalRole struct {
	ID        string `json:"id" gorm:"primaryKey"`
	Principal string `json:"principal" gorm:"index"`
	RoleID    string `json:"role_id" gorm:"index"`
	ProjectID string `json:"project_id"`
	GrantedBy string `json:"granted_by"`
	CreatedAt int64  `json:"created_at"`
}

// ScopeGrant is a direct scope grant outside of roles.
type ScopeGrant struct {
	ID        string `json:"id" gorm:"primaryKey"`
	Principal string `json:"principal" gorm:"index"`
	Scope     string `json:"scope"`
	ProjectID string `json:"project_id"`
	GrantedBy string `json:"granted_by"`
	ExpiresAt int64  `json:"expires_at"` // 0 = no expiry
	CreatedAt int64  `json:"created_at"`
}

// AuditEvent is an audit log entry.
type AuditEvent struct {
	ID        string `json:"id" gorm:"primaryKey"`
	Principal string `json:"principal" gorm:"index"`
	ProjectID string `json:"project_id" gorm:"index"`
	Scope     string `json:"scope"`
	Resource  string `json:"resource"`
	Decision  string `json:"decision"` // "allow" or "deny"
	Reason    string `json:"reason"`
	PrevHash  string `json:"prev_hash"` // hash chain
	Hash      string `json:"hash"`
	RequestID string `json:"request_id"`
	CreatedAt int64  `json:"created_at"`
}

// ElevationGrant is a JIT elevation grant.
type ElevationGrant struct {
	ID          string `json:"id" gorm:"primaryKey"`
	Principal   string `json:"principal" gorm:"index"`
	Scope       string `json:"scope"`
	ProjectID   string `json:"project_id"`
	Status      string `json:"status" gorm:"index"` // pending, approved, expired, revoked, consumed
	Reason      string `json:"reason"`
	ApprovedBy  string `json:"approved_by"`
	ConsumeOnce bool   `json:"consume_once"`
	Consumed    bool   `json:"consumed"`
	RequestTTL  int64  `json:"request_ttl"` // seconds until pending request expires
	ExpiresAt   int64  `json:"expires_at"`  // grant expiry (set on approval)
	CreatedAt   int64  `json:"created_at"`
}
