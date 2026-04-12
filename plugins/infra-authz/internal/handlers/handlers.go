package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/antimatter-studios/teamagentica/plugins/infra-authz/internal/authz"
	"github.com/antimatter-studios/teamagentica/plugins/infra-authz/internal/database"
	"github.com/antimatter-studios/teamagentica/plugins/infra-authz/internal/models"
)

// rateLimiter tracks elevation request timestamps per principal.
type rateLimiter struct {
	mu       sync.Mutex
	requests map[string][]int64 // principal -> list of unix timestamps
}

func newRateLimiter() *rateLimiter {
	rl := &rateLimiter{requests: make(map[string][]int64)}
	go rl.cleanupLoop()
	return rl
}

func (rl *rateLimiter) allow(principal string, maxRequests int, windowSeconds int64) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now().Unix()
	cutoff := now - windowSeconds

	// Filter to only timestamps within the window
	var recent []int64
	for _, ts := range rl.requests[principal] {
		if ts > cutoff {
			recent = append(recent, ts)
		}
	}

	if len(recent) >= maxRequests {
		rl.requests[principal] = recent
		return false
	}

	rl.requests[principal] = append(recent, now)
	return true
}

func (rl *rateLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		now := time.Now().Unix()
		for k, timestamps := range rl.requests {
			var keep []int64
			for _, ts := range timestamps {
				if ts > now-900 {
					keep = append(keep, ts)
				}
			}
			if len(keep) == 0 {
				delete(rl.requests, k)
			} else {
				rl.requests[k] = keep
			}
		}
		rl.mu.Unlock()
	}
}

// jtiTracker prevents token replay by tracking seen JWT IDs.
type jtiTracker struct {
	mu   sync.Mutex
	seen map[string]int64 // jti -> expiry unix timestamp
}

func newJTITracker() *jtiTracker {
	jt := &jtiTracker{seen: make(map[string]int64)}
	go jt.cleanupLoop()
	return jt
}

func (jt *jtiTracker) checkAndRecord(jti string, expiresAt int64) bool {
	jt.mu.Lock()
	defer jt.mu.Unlock()

	if _, exists := jt.seen[jti]; exists {
		return false // replay detected
	}
	jt.seen[jti] = expiresAt
	return true
}

func (jt *jtiTracker) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		jt.mu.Lock()
		now := time.Now().Unix()
		for jti, exp := range jt.seen {
			if exp < now {
				delete(jt.seen, jti)
			}
		}
		jt.mu.Unlock()
	}
}

// Handler serves authz API endpoints.
type Handler struct {
	db            *database.DB
	policy        *authz.PolicyEngine
	tokenService  *authz.TokenService
	expiryMinutes int
	sampleRate    int64
	allowCounter  atomic.Int64
	elevationRL   *rateLimiter
	jtiTracker    *jtiTracker
}

// New creates a new Handler.
func New(db *database.DB, policy *authz.PolicyEngine, ts *authz.TokenService, expiryMinutes int) *Handler {
	return &Handler{
		db:            db,
		policy:        policy,
		tokenService:  ts,
		expiryMinutes: expiryMinutes,
		sampleRate:    10,
		elevationRL:   newRateLimiter(),
		jtiTracker:    newJTITracker(),
	}
}

// SetSampleRate sets the audit sampling rate for allow decisions (every Nth allow is logged).
func (h *Handler) SetSampleRate(rate int64) {
	if rate < 1 {
		rate = 1
	}
	h.sampleRate = rate
}

// Health handles GET /health.
func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// --- Identity registration ---

// RegisterIdentity handles POST /identity/register — called by the kernel
// after a plugin container starts to register its identity and requested scopes.
func (h *Handler) RegisterIdentity(c *gin.Context) {
	var req struct {
		PluginID  string   `json:"plugin_id" binding:"required"`
		Principal string   `json:"principal" binding:"required"`
		ProjectID string   `json:"project_id"`
		AgentType string   `json:"agent_type"`
		Scopes    []string `json:"scopes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	scopesJSON, _ := json.Marshal(req.Scopes)
	identity := &models.PluginIdentity{
		ID:        req.PluginID,
		PluginID:  req.PluginID,
		Principal: req.Principal,
		ProjectID: req.ProjectID,
		AgentType: req.AgentType,
		Scopes:    string(scopesJSON),
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}

	if err := h.db.UpsertIdentity(identity); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "identity registered", "plugin_id": req.PluginID})
}

// --- Token endpoints ---

// MintToken handles POST /token/mint.
func (h *Handler) MintToken(c *gin.Context) {
	var req struct {
		Principal     string   `json:"principal" binding:"required"`
		ProjectID     string   `json:"project_id"`
		AgentType     string   `json:"agent_type"`
		SessionID     string   `json:"session_id"`
		Scopes        []string `json:"scopes"`
		ExpiryMinutes int      `json:"expiry_minutes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	expiry := req.ExpiryMinutes
	if expiry <= 0 {
		expiry = h.expiryMinutes
	}

	token, err := h.tokenService.MintToken(req.Principal, req.ProjectID, req.AgentType, req.SessionID, req.Scopes, expiry)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"token": token})
}

// VerifyToken handles POST /token/verify.
func (h *Handler) VerifyToken(c *gin.Context) {
	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	claims, err := h.tokenService.VerifyToken(req.Token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"valid": false, "error": err.Error()})
		return
	}

	if claims.ID != "" && claims.ExpiresAt != nil {
		if !h.jtiTracker.checkAndRecord(claims.ID, claims.ExpiresAt.Time.Unix()) {
			c.JSON(http.StatusUnauthorized, gin.H{"valid": false, "error": "token replay detected"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"valid":      true,
		"principal":  claims.Principal,
		"project_id": claims.ProjectID,
		"agent_type": claims.AgentType,
		"session_id": claims.SessionID,
		"scopes":     claims.Scopes,
		"expires_at": claims.ExpiresAt.Time.Unix(),
	})
}

// JWKS handles GET /jwks.
func (h *Handler) JWKS(c *gin.Context) {
	c.JSON(http.StatusOK, h.tokenService.JWKS())
}

// --- Policy check ---

// Check handles POST /check.
func (h *Handler) Check(c *gin.Context) {
	var req struct {
		Principal string `json:"principal" binding:"required"`
		Scope     string `json:"scope" binding:"required"`
		Resource  string `json:"resource"`
		ProjectID string `json:"project_id"`
		RequestID string `json:"request_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	decision := h.policy.IsAllowed(req.Principal, req.Scope, req.Resource, req.ProjectID)

	decisionStr := "deny"
	if decision.Allowed {
		decisionStr = "allow"
	}

	requestID := req.RequestID
	if requestID == "" {
		requestID = uuid.New().String()
	}

	// Sampling: always log denies, sample 1-in-N allows.
	shouldLog := !decision.Allowed
	if decision.Allowed {
		count := h.allowCounter.Add(1)
		shouldLog = (count % h.sampleRate) == 0
	}

	if shouldLog {
		now := time.Now().Unix()
		prevHash, _ := h.db.GetLastAuditHash()
		hash := models.AuditHash(prevHash, req.Principal, req.Scope, req.Resource, decisionStr, now)
		audit := &models.AuditEvent{
			ID:        uuid.New().String(),
			Principal: req.Principal,
			ProjectID: req.ProjectID,
			Scope:     req.Scope,
			Resource:  req.Resource,
			Decision:  decisionStr,
			Reason:    decision.Reason,
			PrevHash:  prevHash,
			Hash:      hash,
			RequestID: requestID,
			CreatedAt: now,
		}
		_ = h.db.InsertAudit(audit)
	}

	c.JSON(http.StatusOK, gin.H{
		"allowed":    decision.Allowed,
		"reason":     decision.Reason,
		"request_id": requestID,
	})
}

// --- Scope catalog ---

// ListScopes handles GET /scopes.
func (h *Handler) ListScopes(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"scopes": authz.ScopeCatalog})
}

// --- Roles ---

// ListRoles handles GET /roles.
func (h *Handler) ListRoles(c *gin.Context) {
	roles, err := h.db.ListRoles()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"roles": roles})
}

// CreateRole handles POST /roles.
func (h *Handler) CreateRole(c *gin.Context) {
	var req struct {
		Name        string   `json:"name" binding:"required"`
		Description string   `json:"description"`
		Scopes      []string `json:"scopes" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	scopesJSON, _ := json.Marshal(req.Scopes)
	role := &models.Role{
		ID:          uuid.New().String(),
		Name:        req.Name,
		Description: req.Description,
		Scopes:      string(scopesJSON),
		CreatedAt:   time.Now().Unix(),
		UpdatedAt:   time.Now().Unix(),
	}

	if err := h.db.CreateRole(role); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, role)
}

// UpdateRole handles PUT /roles/:id.
func (h *Handler) UpdateRole(c *gin.Context) {
	id := c.Param("id")
	role, err := h.db.GetRole(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "role not found"})
		return
	}

	var req struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Scopes      []string `json:"scopes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Name != "" {
		role.Name = req.Name
	}
	if req.Description != "" {
		role.Description = req.Description
	}
	if req.Scopes != nil {
		scopesJSON, _ := json.Marshal(req.Scopes)
		role.Scopes = string(scopesJSON)
	}
	role.UpdatedAt = time.Now().Unix()

	if err := h.db.UpdateRole(role); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, role)
}

// --- Grants ---

// CreateGrant handles POST /grants.
func (h *Handler) CreateGrant(c *gin.Context) {
	var req struct {
		Principal string `json:"principal" binding:"required"`
		Scope     string `json:"scope" binding:"required"`
		ProjectID string `json:"project_id"`
		GrantedBy string `json:"granted_by"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	grant := &models.ScopeGrant{
		ID:        uuid.New().String(),
		Principal: req.Principal,
		Scope:     req.Scope,
		ProjectID: req.ProjectID,
		GrantedBy: req.GrantedBy,
		ExpiresAt: req.ExpiresAt,
		CreatedAt: time.Now().Unix(),
	}

	if err := h.db.CreateGrant(grant); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, grant)
}

// --- Audit ---

// ListAudit handles GET /audit.
func (h *Handler) ListAudit(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", "100")
	limit, _ := strconv.Atoi(limitStr)
	sinceStr := c.Query("since")
	var since int64
	if sinceStr != "" {
		since, _ = strconv.ParseInt(sinceStr, 10, 64)
	}

	filter := database.AuditFilter{
		Principal: c.Query("principal"),
		ProjectID: c.Query("project_id"),
		Decision:  c.Query("decision"),
		Scope:     c.Query("scope"),
		Since:     since,
		Limit:     limit,
	}

	events, err := h.db.ListAuditEventsFiltered(filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": events})
}

// AuditStats handles GET /audit/stats.
func (h *Handler) AuditStats(c *gin.Context) {
	stats, err := h.db.AuditStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, stats)
}

// AuditVerify handles GET /audit/verify.
func (h *Handler) AuditVerify(c *gin.Context) {
	result, err := h.db.AuditVerifyChain()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// AuditReport handles POST /audit/report — receives audit events from SDK enforcement.
func (h *Handler) AuditReport(c *gin.Context) {
	var req struct {
		Principal string `json:"principal"`
		Scope     string `json:"scope"`
		Resource  string `json:"resource"`
		ProjectID string `json:"project_id"`
		Decision  string `json:"decision"`
		Reason    string `json:"reason"`
		RequestID string `json:"request_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	now := time.Now().Unix()
	prevHash, _ := h.db.GetLastAuditHash()
	hash := models.AuditHash(prevHash, req.Principal, req.Scope, req.Resource, req.Decision, now)

	requestID := req.RequestID
	if requestID == "" {
		requestID = uuid.New().String()
	}

	audit := &models.AuditEvent{
		ID:        uuid.New().String(),
		Principal: req.Principal,
		ProjectID: req.ProjectID,
		Scope:     req.Scope,
		Resource:  req.Resource,
		Decision:  req.Decision,
		Reason:    req.Reason,
		PrevHash:  prevHash,
		Hash:      hash,
		RequestID: requestID,
		CreatedAt: now,
	}
	_ = h.db.InsertAudit(audit)

	c.JSON(http.StatusOK, gin.H{"status": "recorded"})
}

// --- Elevation ---

// consumeOnceScopes are destructive one-shot scopes that auto-set ConsumeOnce.
var consumeOnceScopes = map[string]bool{
	"workspace.delete": true,
	"plugin.stop":      true,
}

// RequestElevation handles POST /elevation/request.
func (h *Handler) RequestElevation(c *gin.Context) {
	var req struct {
		Principal string `json:"principal" binding:"required"`
		Scope     string `json:"scope" binding:"required"`
		ProjectID string `json:"project_id"`
		Reason    string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !h.elevationRL.allow(req.Principal, 5, 900) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded: max 5 elevation requests per 15 minutes"})
		return
	}

	def, ok := authz.ScopeCatalog[req.Scope]
	if !ok || !def.JITRequired {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scope does not require elevation"})
		return
	}

	grant := &models.ElevationGrant{
		ID:          uuid.New().String(),
		Principal:   req.Principal,
		Scope:       req.Scope,
		ProjectID:   req.ProjectID,
		Status:      "pending",
		Reason:      req.Reason,
		ConsumeOnce: consumeOnceScopes[req.Scope],
		Consumed:    false,
		RequestTTL:  900,
		CreatedAt:   time.Now().Unix(),
	}

	if err := h.db.CreateElevationGrant(grant); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"grant_id": grant.ID, "status": "pending"})
}

// ApproveElevation handles POST /elevation/approve.
func (h *Handler) ApproveElevation(c *gin.Context) {
	var req struct {
		GrantID    string `json:"grant_id" binding:"required"`
		ApprovedBy string `json:"approved_by" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	grant, err := h.db.GetElevationGrant(req.GrantID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "grant not found"})
		return
	}

	if grant.Status != "pending" {
		c.JSON(http.StatusConflict, gin.H{"error": "grant is not pending", "status": grant.Status})
		return
	}

	if grant.RequestTTL > 0 && time.Now().Unix() > grant.CreatedAt+grant.RequestTTL {
		_ = h.db.Gorm().Model(grant).Update("status", "expired")
		c.JSON(http.StatusConflict, gin.H{"error": "elevation request has expired"})
		return
	}

	expiresAt := time.Now().Add(30 * time.Minute).Unix()
	if err := h.db.ApproveElevationGrant(req.GrantID, req.ApprovedBy, expiresAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "approved", "expires_at": expiresAt})
}

// ListElevationGrants handles GET /elevation/grants.
func (h *Handler) ListElevationGrants(c *gin.Context) {
	grants, err := h.db.ListElevationGrants(c.Query("principal"), c.Query("status"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"grants": grants})
}

// GetElevationGrant handles GET /elevation/grants/:id.
func (h *Handler) GetElevationGrant(c *gin.Context) {
	grant, err := h.db.GetElevationGrant(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "grant not found"})
		return
	}
	c.JSON(http.StatusOK, grant)
}

// RevokeElevation handles POST /elevation/revoke.
func (h *Handler) RevokeElevation(c *gin.Context) {
	var req struct {
		GrantID string `json:"grant_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.db.RevokeElevationGrant(req.GrantID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "revoked"})
}

// --- MCP tool definitions ---

// ToolDefs returns the MCP tool definitions.
func (h *Handler) ToolDefs() interface{} {
	return []gin.H{
		{
			"name":        "check_scope",
			"description": "Check if a principal is allowed to perform a scope on a resource",
			"endpoint":    "/mcp/check_scope",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"principal":  gin.H{"type": "string", "description": "Principal identifier (e.g. agent:project:instance)"},
					"scope":      gin.H{"type": "string", "description": "Scope to check (e.g. memory.read)"},
					"resource":   gin.H{"type": "string", "description": "Resource identifier (optional)"},
					"project_id": gin.H{"type": "string", "description": "Project ID (optional)"},
				},
				"required": []string{"principal", "scope"},
			},
		},
		{
			"name":        "mint_token",
			"description": "Mint a JWT token for a principal with specified scopes",
			"endpoint":    "/mcp/mint_token",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"principal":  gin.H{"type": "string", "description": "Principal identifier"},
					"project_id": gin.H{"type": "string", "description": "Project ID"},
					"scopes":     gin.H{"type": "array", "items": gin.H{"type": "string"}, "description": "Scopes to include in the token"},
				},
				"required": []string{"principal"},
			},
		},
		{
			"name":        "list_scopes",
			"description": "List all available authorization scopes",
			"endpoint":    "/mcp/list_scopes",
			"parameters":  gin.H{"type": "object", "properties": gin.H{}},
		},
	}
}

// --- MCP handlers ---

// MCPCheckScope handles POST /mcp/check_scope.
func (h *Handler) MCPCheckScope(c *gin.Context) {
	var req struct {
		Principal string `json:"principal"`
		Scope     string `json:"scope"`
		Resource  string `json:"resource"`
		ProjectID string `json:"project_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Principal == "" || req.Scope == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "principal and scope required"})
		return
	}

	decision := h.policy.IsAllowed(req.Principal, req.Scope, req.Resource, req.ProjectID)
	c.JSON(http.StatusOK, gin.H{
		"allowed": decision.Allowed,
		"reason":  decision.Reason,
	})
}

// MCPMintToken handles POST /mcp/mint_token.
func (h *Handler) MCPMintToken(c *gin.Context) {
	var req struct {
		Principal string   `json:"principal"`
		ProjectID string   `json:"project_id"`
		Scopes    []string `json:"scopes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Principal == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "principal required"})
		return
	}

	token, err := h.tokenService.MintToken(req.Principal, req.ProjectID, "", "", req.Scopes, h.expiryMinutes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token})
}

// MCPListScopes handles POST /mcp/list_scopes.
func (h *Handler) MCPListScopes(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"scopes": authz.ScopeCatalog})
}

// GetTools handles GET /mcp.
func (h *Handler) GetTools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": h.ToolDefs()})
}
