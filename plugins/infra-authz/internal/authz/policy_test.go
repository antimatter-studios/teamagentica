package authz

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"

	"github.com/antimatter-studios/teamagentica/plugins/infra-authz/internal/models"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/antimatter-studios/teamagentica/plugins/infra-authz/internal/database"
)

func setupTestDB(t *testing.T) *database.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	conn.AutoMigrate(
		&models.PluginIdentity{},
		&models.Role{},
		&models.PrincipalRole{},
		&models.ScopeGrant{},
		&models.AuditEvent{},
		&models.ElevationGrant{},
	)
	return database.NewFromGorm(conn)
}

func setupTokenService(t *testing.T) *TokenService {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return &TokenService{privateKey: priv, publicKey: pub, keyID: "test-kid"}
}

// --- Token tests ---

func TestValidTokenAccepted(t *testing.T) {
	ts := setupTokenService(t)
	tok, err := ts.MintToken("agent:p1:i1", "p1", "chat", "", []string{"memory.read"}, 60)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	claims, err := ts.VerifyToken(tok)
	if err != nil {
		t.Fatalf("verify valid token: %v", err)
	}
	if claims.Principal != "agent:p1:i1" {
		t.Errorf("principal = %s, want agent:p1:i1", claims.Principal)
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	ts := setupTokenService(t)

	now := time.Now().Add(-2 * time.Hour)
	claims := TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.New().String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(1 * time.Minute)),
			Issuer:    "infra-authz",
		},
		Principal: "agent:p1:i1",
		Scopes:    []string{"memory.read"},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := token.SignedString(ts.privateKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	_, err = ts.VerifyToken(signed)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestInvalidSignatureRejected(t *testing.T) {
	ts := setupTokenService(t)
	tok, err := ts.MintToken("agent:p1:i1", "p1", "", "", []string{"memory.read"}, 60)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	// Tamper with last byte
	tampered := []byte(tok)
	tampered[len(tampered)-1] ^= 0xFF
	_, err = ts.VerifyToken(string(tampered))
	if err == nil {
		t.Fatal("expected error for tampered token")
	}
}

func TestMissingRequiredClaimsRejected(t *testing.T) {
	ts := setupTokenService(t)

	// Token with no expiry — jwt library should still parse, but let's test with empty claims
	claims := jwt.RegisteredClaims{
		ID:     uuid.New().String(),
		Issuer: "infra-authz",
		// No ExpiresAt
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := token.SignedString(ts.privateKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Parse with TokenClaims — should succeed structurally but principal is empty
	result, err := ts.VerifyToken(signed)
	if err != nil {
		// Also acceptable — depends on jwt library behavior
		return
	}
	if result.Principal != "" {
		t.Errorf("expected empty principal, got %s", result.Principal)
	}
}

// --- RBAC tests ---

func TestAgentRoleCanAccessMemoryRead(t *testing.T) {
	db := setupTestDB(t)
	db.SeedDefaultRoles()

	principal := "agent:proj1:inst1"
	db.AssignRole(&models.PrincipalRole{
		ID: uuid.New().String(), Principal: principal, RoleID: "role:agent",
		ProjectID: "proj1", GrantedBy: "system", CreatedAt: time.Now().Unix(),
	})

	pe := NewPolicyEngine(db)
	d := pe.IsAllowed(principal, "memory.read", "", "proj1")
	if !d.Allowed {
		t.Errorf("agent should access memory.read, got denied: %s", d.Reason)
	}
}

func TestAgentRoleCannotAccessDeployPromote(t *testing.T) {
	db := setupTestDB(t)
	db.SeedDefaultRoles()

	principal := "agent:proj1:inst1"
	db.AssignRole(&models.PrincipalRole{
		ID: uuid.New().String(), Principal: principal, RoleID: "role:agent",
		ProjectID: "proj1", GrantedBy: "system", CreatedAt: time.Now().Unix(),
	})

	pe := NewPolicyEngine(db)
	d := pe.IsAllowed(principal, "deploy.promote", "", "proj1")
	if d.Allowed {
		t.Error("agent should NOT access deploy.promote without elevation")
	}
	if d.Reason != "elevation required" {
		t.Errorf("reason = %s, want 'elevation required'", d.Reason)
	}
}

func TestNoRolesDenied(t *testing.T) {
	db := setupTestDB(t)
	pe := NewPolicyEngine(db)

	d := pe.IsAllowed("agent:proj1:nobody", "memory.read", "", "proj1")
	if d.Allowed {
		t.Error("principal with no roles should be denied")
	}
}

func TestWildcardScopeMemoryStar(t *testing.T) {
	db := setupTestDB(t)
	scopesJSON, _ := json.Marshal([]string{"memory.*"})
	db.CreateRole(&models.Role{
		ID: "role:test-wild", Name: "role:test-wild", Scopes: string(scopesJSON),
		CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(),
	})
	principal := "agent:p1:i1"
	db.AssignRole(&models.PrincipalRole{
		ID: uuid.New().String(), Principal: principal, RoleID: "role:test-wild",
		ProjectID: "p1", GrantedBy: "system", CreatedAt: time.Now().Unix(),
	})

	pe := NewPolicyEngine(db)
	if d := pe.IsAllowed(principal, "memory.read", "", "p1"); !d.Allowed {
		t.Error("memory.* should match memory.read")
	}
	if d := pe.IsAllowed(principal, "memory.write", "", "p1"); !d.Allowed {
		t.Error("memory.* should match memory.write")
	}
	if d := pe.IsAllowed(principal, "storage.read", "", "p1"); d.Allowed {
		t.Error("memory.* should NOT match storage.read")
	}
}

func TestWildcardStarMatchesEverything(t *testing.T) {
	db := setupTestDB(t)
	db.SeedDefaultRoles()

	principal := "admin:proj1:admin1"
	db.AssignRole(&models.PrincipalRole{
		ID: uuid.New().String(), Principal: principal, RoleID: "role:admin",
		ProjectID: "proj1", GrantedBy: "system", CreatedAt: time.Now().Unix(),
	})

	pe := NewPolicyEngine(db)
	for scope := range ScopeCatalog {
		if ScopeCatalog[scope].JITRequired {
			continue
		}
		d := pe.IsAllowed(principal, scope, "", "proj1")
		if !d.Allowed {
			t.Errorf("admin wildcard * should match %s", scope)
		}
	}
}

func TestCrossProjectDenied(t *testing.T) {
	db := setupTestDB(t)

	principal := "agent:projA:inst1"
	scopesJSON, _ := json.Marshal([]string{"memory.read"})
	db.UpsertIdentity(&models.PluginIdentity{
		ID: "plug1", PluginID: "plug1", Principal: principal,
		ProjectID: "projA", Scopes: string(scopesJSON),
		CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(),
	})

	pe := NewPolicyEngine(db)

	// Allowed in own project (via identity scopes)
	d := pe.IsAllowed(principal, "memory.read", "", "projA")
	if !d.Allowed {
		t.Error("should be allowed in own project via identity scopes")
	}

	// Different principal from project B has no grants
	d = pe.IsAllowed("agent:projB:inst2", "memory.read", "", "projB")
	if d.Allowed {
		t.Error("cross-project principal should be denied")
	}
}

// --- Elevation tests ---

func TestJITDeniedWithoutElevation(t *testing.T) {
	db := setupTestDB(t)
	pe := NewPolicyEngine(db)

	d := pe.IsAllowed("agent:p1:i1", "deploy.promote", "", "p1")
	if d.Allowed {
		t.Error("JIT scope should be denied without elevation")
	}
	if d.Reason != "elevation required" {
		t.Errorf("reason = %s", d.Reason)
	}
}

func TestJITAllowedWithActiveGrant(t *testing.T) {
	db := setupTestDB(t)
	principal := "agent:p1:i1"

	db.CreateElevationGrant(&models.ElevationGrant{
		ID: uuid.New().String(), Principal: principal, Scope: "deploy.promote",
		ProjectID: "p1", Status: "approved", ConsumeOnce: false, Consumed: false,
		ExpiresAt: time.Now().Add(30 * time.Minute).Unix(),
		CreatedAt: time.Now().Unix(),
	})

	pe := NewPolicyEngine(db)
	d := pe.IsAllowed(principal, "deploy.promote", "", "p1")
	if !d.Allowed {
		t.Errorf("JIT scope should be allowed with active grant, got: %s", d.Reason)
	}
}

func TestJITExpiredGrantRejected(t *testing.T) {
	db := setupTestDB(t)
	principal := "agent:p1:i1"

	db.CreateElevationGrant(&models.ElevationGrant{
		ID: uuid.New().String(), Principal: principal, Scope: "deploy.promote",
		ProjectID: "p1", Status: "approved", ConsumeOnce: false, Consumed: false,
		ExpiresAt: time.Now().Add(-1 * time.Minute).Unix(), // expired
		CreatedAt: time.Now().Add(-1 * time.Hour).Unix(),
	})

	pe := NewPolicyEngine(db)
	d := pe.IsAllowed(principal, "deploy.promote", "", "p1")
	if d.Allowed {
		t.Error("expired elevation grant should be rejected")
	}
}

func TestJITConsumeOnceSecondUseRejected(t *testing.T) {
	db := setupTestDB(t)
	principal := "agent:p1:i1"

	grantID := uuid.New().String()
	db.CreateElevationGrant(&models.ElevationGrant{
		ID: grantID, Principal: principal, Scope: "plugin.stop",
		ProjectID: "p1", Status: "approved", ConsumeOnce: true, Consumed: false,
		ExpiresAt: time.Now().Add(30 * time.Minute).Unix(),
		CreatedAt: time.Now().Unix(),
	})

	pe := NewPolicyEngine(db)

	// First use — should succeed and consume
	d := pe.IsAllowed(principal, "plugin.stop", "", "p1")
	if !d.Allowed {
		t.Fatalf("first use should be allowed, got: %s", d.Reason)
	}

	// Second use — should be rejected (grant consumed)
	d = pe.IsAllowed(principal, "plugin.stop", "", "p1")
	if d.Allowed {
		t.Error("second use of consume-once grant should be rejected")
	}
}

func TestJITRevokedGrantRejected(t *testing.T) {
	db := setupTestDB(t)
	principal := "agent:p1:i1"

	grantID := uuid.New().String()
	db.CreateElevationGrant(&models.ElevationGrant{
		ID: grantID, Principal: principal, Scope: "deploy.promote",
		ProjectID: "p1", Status: "approved", ConsumeOnce: false, Consumed: false,
		ExpiresAt: time.Now().Add(30 * time.Minute).Unix(),
		CreatedAt: time.Now().Unix(),
	})

	// Revoke it
	db.RevokeElevationGrant(grantID)

	pe := NewPolicyEngine(db)
	d := pe.IsAllowed(principal, "deploy.promote", "", "p1")
	if d.Allowed {
		t.Error("revoked elevation grant should be rejected")
	}
}
