package authz

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/antimatter-studios/teamagentica/plugins/infra-authz/internal/database"
)

// Decision is the result of a policy evaluation.
type Decision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

// PolicyEngine evaluates authorization decisions.
type PolicyEngine struct {
	db *database.DB
}

// NewPolicyEngine creates a new policy engine.
func NewPolicyEngine(db *database.DB) *PolicyEngine {
	return &PolicyEngine{db: db}
}

// IsAllowed checks if a principal is allowed to perform the given scope on a resource.
func (p *PolicyEngine) IsAllowed(principal, scope, resource, projectID string) Decision {
	// 1. Check if scope requires JIT elevation
	if def, ok := ScopeCatalog[scope]; ok && def.JITRequired {
		grant, err := p.db.FindActiveGrant(principal, scope, projectID)
		if err != nil || grant == nil {
			return Decision{Allowed: false, Reason: "elevation required"}
		}
		if grant.ConsumeOnce {
			_ = p.db.ConsumeElevationGrant(grant.ID)
		}
		return Decision{Allowed: true, Reason: "JIT elevation grant"}
	}

	// 2. Collect scopes from roles
	roleScopes := p.collectRoleScopes(principal)

	// 3. Collect direct grants
	directScopes := p.collectDirectGrants(principal)

	// 4. Union all scopes
	allScopes := make(map[string]bool)
	for _, s := range roleScopes {
		allScopes[s] = true
	}
	for _, s := range directScopes {
		allScopes[s] = true
	}

	// 5. Check if requested scope matches any granted scope (with wildcard support)
	for granted := range allScopes {
		if matchScope(granted, scope) {
			return Decision{Allowed: true, Reason: "granted via role or direct grant"}
		}
	}

	// 6. Check identity scopes
	identity, err := p.db.GetIdentityByPrincipal(principal)
	if err == nil && identity != nil {
		var idScopes []string
		if err := json.Unmarshal([]byte(identity.Scopes), &idScopes); err == nil {
			for _, s := range idScopes {
				if matchScope(s, scope) {
					return Decision{Allowed: true, Reason: "granted via identity scopes"}
				}
			}
		}
	}

	return Decision{Allowed: false, Reason: "no matching grant found"}
}

// collectRoleScopes returns all scope patterns from roles assigned to the principal.
func (p *PolicyEngine) collectRoleScopes(principal string) []string {
	prs, err := p.db.GetPrincipalRoles(principal)
	if err != nil {
		return nil
	}

	var scopes []string
	for _, pr := range prs {
		role, err := p.db.GetRole(pr.RoleID)
		if err != nil {
			continue
		}
		var roleScopes []string
		if err := json.Unmarshal([]byte(role.Scopes), &roleScopes); err == nil {
			scopes = append(scopes, roleScopes...)
		}
	}
	return scopes
}

// collectDirectGrants returns all non-expired direct scope grants.
func (p *PolicyEngine) collectDirectGrants(principal string) []string {
	grants, err := p.db.GetPrincipalGrants(principal)
	if err != nil {
		return nil
	}

	now := time.Now().Unix()
	var scopes []string
	for _, g := range grants {
		if g.ExpiresAt > 0 && g.ExpiresAt <= now {
			continue // expired
		}
		scopes = append(scopes, g.Scope)
	}
	return scopes
}

// matchScope checks if a granted scope pattern matches the requested scope.
// Supports wildcard: "memory.*" matches "memory.read", "*" matches everything.
func matchScope(pattern, scope string) bool {
	if pattern == "*" {
		return true
	}
	if pattern == scope {
		return true
	}
	// Wildcard: "memory.*" matches "memory.read"
	if strings.HasSuffix(pattern, ".*") {
		prefix := strings.TrimSuffix(pattern, ".*")
		if strings.HasPrefix(scope, prefix+".") {
			return true
		}
	}
	return false
}
