package pluginsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// tokenCache manages a cached JWT token fetched from infra-authz.
// Thread-safe; automatically refreshes before expiry.
type tokenCache struct {
	mu       sync.RWMutex
	token    string
	expiry   time.Time
	identity Identity

	refreshing   bool
	refreshMu    sync.Mutex
	client       *Client
	fetchedOnce  bool
	unavailable  bool
	retryAfter   time.Time
}

// newTokenCache creates a token cache for the given SDK client.
func newTokenCache(c *Client) *tokenCache {
	return &tokenCache{
		identity: GetIdentity(),
		client:   c,
	}
}

// getToken returns a valid cached token, or fetches/refreshes as needed.
// Returns empty string if infra-authz is unavailable (graceful degradation).
func (tc *tokenCache) getToken() string {
	tc.mu.RLock()
	token := tc.token
	expiry := tc.expiry
	unavailable := tc.unavailable
	retryAfter := tc.retryAfter
	tc.mu.RUnlock()

	// If we have a valid token with >20% lifetime remaining, use it.
	if token != "" && time.Now().Before(expiry) {
		return token
	}

	// If authz was unavailable, don't retry until backoff expires.
	if unavailable && time.Now().Before(retryAfter) {
		return ""
	}

	// No identity principal means we can't mint tokens.
	if tc.identity.Principal == "" {
		return ""
	}

	// Need to fetch or refresh — only one goroutine does this.
	tc.refreshMu.Lock()
	defer tc.refreshMu.Unlock()

	// Double-check after acquiring lock.
	tc.mu.RLock()
	if tc.token != "" && time.Now().Before(tc.expiry) {
		t := tc.token
		tc.mu.RUnlock()
		return t
	}
	tc.mu.RUnlock()

	tc.fetchToken()

	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.token
}

// fetchToken calls infra-authz POST /token/mint and caches the result.
func (tc *tokenCache) fetchToken() {
	expiryMinutes := 60
	payload := map[string]interface{}{
		"principal":      tc.identity.Principal,
		"project_id":     tc.identity.ProjectID,
		"agent_type":     tc.identity.AgentType,
		"session_id":     tc.identity.SessionID,
		"expiry_minutes": expiryMinutes,
	}
	body, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := tc.client.RouteToPluginNoAuthz(ctx, "infra-authz", "POST", "/token/mint", bytes.NewReader(body))
	if err != nil {
		if !tc.fetchedOnce {
			log.Printf("pluginsdk: authz token fetch failed (infra-authz may not be running yet): %v", err)
		}
		tc.mu.Lock()
		tc.unavailable = true
		tc.retryAfter = time.Now().Add(30 * time.Second)
		tc.mu.Unlock()
		return
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(resp, &result); err != nil || result.Token == "" {
		log.Printf("pluginsdk: authz token response invalid: %v", err)
		tc.mu.Lock()
		tc.unavailable = true
		tc.retryAfter = time.Now().Add(30 * time.Second)
		tc.mu.Unlock()
		return
	}

	// Cache with 80% of the expiry window.
	refreshAt := time.Now().Add(time.Duration(float64(expiryMinutes)*0.8) * time.Minute)

	tc.mu.Lock()
	tc.token = result.Token
	tc.expiry = refreshAt
	tc.unavailable = false
	tc.fetchedOnce = true
	tc.mu.Unlock()

	if !tc.fetchedOnce {
		log.Printf("pluginsdk: authz token acquired (principal=%s)", tc.identity.Principal)
	}
}

// scheduleRefresh starts a background goroutine that refreshes the token
// before it expires. Called once after the first successful fetch.
func (tc *tokenCache) startBackgroundRefresh(stopCh <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				tc.mu.RLock()
				needsRefresh := tc.token != "" && time.Now().After(tc.expiry.Add(-2*time.Minute))
				tc.mu.RUnlock()

				if needsRefresh {
					tc.refreshMu.Lock()
					tc.fetchToken()
					tc.refreshMu.Unlock()
				}

				// Also retry if authz became unavailable.
				tc.mu.RLock()
				shouldRetry := tc.unavailable && time.Now().After(tc.retryAfter)
				tc.mu.RUnlock()

				if shouldRetry {
					tc.refreshMu.Lock()
					tc.fetchToken()
					tc.refreshMu.Unlock()
				}
			}
		}
	}()
}

// authzTransport wraps an http.RoundTripper to inject Authorization headers
// on outgoing requests using the cached JWT token.
type authzTransport struct {
	base  http.RoundTripper
	cache *tokenCache
}

func (t *authzTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Don't override an existing Authorization header.
	if req.Header.Get("Authorization") == "" {
		if token := t.cache.getToken(); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	return t.base.RoundTrip(req)
}

// wrapTransportWithAuthz wraps an existing transport with auth token injection.
func wrapTransportWithAuthz(base http.RoundTripper, cache *tokenCache) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &authzTransport{base: base, cache: cache}
}

// initAuthz sets up the token cache and wraps the SDK's HTTP clients.
// Called from NewClient after the clients are created.
func (c *Client) initAuthz() {
	identity := GetIdentity()
	if identity.Principal == "" {
		return
	}

	c.tokenCache = newTokenCache(c)
	c.tokenCache.startBackgroundRefresh(c.stopCh)

	// Wrap both HTTP clients to inject auth headers on outgoing requests.
	c.routeClient.Transport = wrapTransportWithAuthz(c.routeClient.Transport, c.tokenCache)
}

// RequireAuthz returns HTTP middleware that validates incoming request tokens.
// Opt-in — plugins that want to enforce authorization call this explicitly.
//
// Usage with standard mux:
//
//	mux.Handle("/sensitive", sdkClient.RequireAuthz()(sensitiveHandler))
//
// Usage with Gin:
//
//	router.Use(gin.WrapH(sdkClient.RequireAuthzMiddleware()))
func (c *Client) RequireAuthz() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
				return
			}

			// Strip "Bearer " prefix.
			token := authHeader
			if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
				token = authHeader[7:]
			}

			// Verify via infra-authz.
			verifyPayload, _ := json.Marshal(map[string]string{"token": token})
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()

			resp, err := c.RouteToPlugin(ctx, "infra-authz", "POST", "/token/verify", bytes.NewReader(verifyPayload))
			if err != nil {
				// Authz unavailable — allow through (graceful degradation).
				log.Printf("pluginsdk: authz verification failed (allowing): %v", err)
				next.ServeHTTP(w, r)
				return
			}

			var result struct {
				Valid     bool   `json:"valid"`
				Error     string `json:"error,omitempty"`
				Principal string `json:"principal,omitempty"`
			}
			if err := json.Unmarshal(resp, &result); err != nil || !result.Valid {
				errMsg := "invalid token"
				if result.Error != "" {
					errMsg = result.Error
				}
				go c.reportAuditDecision(result.Principal, r.URL.Path, "deny", errMsg)
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, errMsg), http.StatusForbidden)
				return
			}

			go c.reportAuditDecision(result.Principal, r.URL.Path, "allow", "token verified")

			// Set principal on request context for downstream use.
			ctx2 := context.WithValue(r.Context(), authzPrincipalKey, result.Principal)
			next.ServeHTTP(w, r.WithContext(ctx2))
		})
	}
}

type contextKey string

const authzPrincipalKey contextKey = "authz_principal"

// AuthzPrincipal extracts the verified principal from the request context.
// Returns empty string if no principal was set (e.g. authz not enforced).
func AuthzPrincipal(r *http.Request) string {
	v, _ := r.Context().Value(authzPrincipalKey).(string)
	return v
}

// RequireAuthzMiddleware returns an http.Handler middleware for use with
// frameworks that accept http.Handler (e.g. gin.WrapH).
func (c *Client) RequireAuthzMiddleware() func(http.Handler) http.Handler {
	return c.RequireAuthz()
}

// reportAuditDecision sends a fire-and-forget audit report to infra-authz.
func (c *Client) reportAuditDecision(principal, resource, decision, reason string) {
	identity := GetIdentity()
	payload, _ := json.Marshal(map[string]string{
		"principal":  principal,
		"resource":   resource,
		"scope":      "token.verify",
		"project_id": identity.ProjectID,
		"decision":   decision,
		"reason":     reason,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := c.RouteToPlugin(ctx, "infra-authz", "POST", "/audit/report", bytes.NewReader(payload))
	if err != nil {
		log.Printf("pluginsdk: audit report failed: %v", err)
	}
}

