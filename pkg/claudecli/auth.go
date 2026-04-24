package claudecli

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	claudeClientID    = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	claudeAuthURL     = "https://claude.ai/oauth/authorize"
	claudeTokenURL    = "https://platform.claude.com/v1/oauth/token"
	claudeRedirectURI = "https://platform.claude.com/oauth/code/callback"
	claudeScopes      = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
)

// LoginResult holds the login URL for the UI.
type LoginResult struct {
	URL string `json:"url"`
}

// authStatus is the JSON output of `claude auth status --json`.
type authStatus struct {
	LoggedIn         bool   `json:"loggedIn"`
	AuthMethod       string `json:"authMethod"`
	Email            string `json:"email"`
	SubscriptionType string `json:"subscriptionType"`
}

// pkceState holds the PKCE parameters for a login in progress.
type pkceState struct {
	codeVerifier string
	state        string
}

// authState holds mutable per-Client login state.
type authState struct {
	mu              sync.Mutex
	loginInProgress bool
	pkce            *pkceState
}

// per-client auth state, keyed by client pointer.
var (
	authStates   = map[*Client]*authState{}
	authStatesMu sync.Mutex
)

func getAuthState(c *Client) *authState {
	authStatesMu.Lock()
	defer authStatesMu.Unlock()
	s, ok := authStates[c]
	if !ok {
		s = &authState{}
		authStates[c] = s
	}
	return s
}

// IsAuthenticated checks if the Claude CLI has stored credentials.
// Triggers a proactive refresh first so an expired access token doesn't
// surface as logged-out when we still hold a valid refresh token.
func (c *Client) IsAuthenticated() bool {
	if err := c.EnsureFreshAuth(); err != nil {
		log.Printf("[claude-cli] IsAuthenticated: refresh attempt failed: %v", err)
	}
	cmd := exec.Command(c.binary, "auth", "status", "--json")
	cmd.Env = c.env()
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	var status authStatus
	if json.Unmarshal(out, &status) != nil {
		return false
	}
	return status.LoggedIn
}

// IsLoginInProgress returns true while a login flow is active.
func (c *Client) IsLoginInProgress() bool {
	as := getAuthState(c)
	as.mu.Lock()
	defer as.mu.Unlock()
	return as.loginInProgress
}

// AuthStatusInfo returns structured auth status.
func (c *Client) AuthStatusInfo() (*authStatus, error) {
	if err := c.EnsureFreshAuth(); err != nil {
		log.Printf("[claude-cli] AuthStatusInfo: refresh attempt failed: %v", err)
	}
	cmd := exec.Command(c.binary, "auth", "status", "--json")
	cmd.Env = c.env()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("auth status: %w", err)
	}
	var status authStatus
	if err := json.Unmarshal(out, &status); err != nil {
		return nil, fmt.Errorf("parse auth status: %w", err)
	}
	return &status, nil
}

// EnsureFreshAuth checks the stored credentials and refreshes the access
// token if it's within the skew window of expiry. Safe to call concurrently
// — refreshes are serialized by c.refreshMu.
//
// Returns nil when there are no credentials to refresh, when the token is
// still valid, or when a refresh completes. Returns an error only if the
// refresh HTTP call itself fails (e.g. network error or refresh token
// revoked).
func (c *Client) EnsureFreshAuth() error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	creds, err := c.readCredentials()
	if err != nil {
		return fmt.Errorf("read credentials: %w", err)
	}
	if creds == nil || creds.ClaudeAiOauth == nil {
		return nil
	}
	oat := creds.ClaudeAiOauth
	if oat.RefreshToken == "" {
		return nil
	}

	// Refresh when within 60s of expiry or already expired.
	const skewMillis = int64(60 * 1000)
	now := time.Now().UnixMilli()
	if oat.ExpiresAt > now+skewMillis {
		return nil
	}

	log.Printf("[claude-cli] EnsureFreshAuth: access token near expiry (expiresAt=%d now=%d), refreshing", oat.ExpiresAt, now)
	newTokens, err := c.refreshTokens(oat.RefreshToken)
	if err != nil {
		return fmt.Errorf("refresh tokens: %w", err)
	}

	updated := *oat
	updated.AccessToken = newTokens.AccessToken
	if newTokens.RefreshToken != "" {
		updated.RefreshToken = newTokens.RefreshToken
	}
	if newTokens.ExpiresIn > 0 {
		updated.ExpiresAt = time.Now().Add(time.Duration(newTokens.ExpiresIn) * time.Second).UnixMilli()
	}
	if newTokens.Scope != "" {
		updated.Scopes = strings.Fields(newTokens.Scope)
	}

	if err := c.writeCredentialsAtomic(&updated); err != nil {
		return fmt.Errorf("write refreshed credentials: %w", err)
	}
	log.Printf("[claude-cli] EnsureFreshAuth: refreshed, new expiresAt=%d", updated.ExpiresAt)
	return nil
}

// StartLogin generates a PKCE authorization URL and returns it.
// No CLI process is started — the full OAuth exchange happens in SubmitCode.
func (c *Client) StartLogin() (*LoginResult, error) {
	as := getAuthState(c)
	as.mu.Lock()
	defer as.mu.Unlock()

	if as.loginInProgress && as.pkce != nil {
		// Return existing URL.
		u := buildAuthURL(as.pkce.state, computeChallenge(as.pkce.codeVerifier))
		log.Printf("[claude-cli] StartLogin: returning existing OAuth URL")
		return &LoginResult{URL: u}, nil
	}

	verifier, err := generateRandom(32)
	if err != nil {
		return nil, fmt.Errorf("generate verifier: %w", err)
	}
	state, err := generateRandom(32)
	if err != nil {
		return nil, fmt.Errorf("generate state: %w", err)
	}

	as.pkce = &pkceState{codeVerifier: verifier, state: state}
	as.loginInProgress = true

	authURL := buildAuthURL(state, computeChallenge(verifier))
	log.Printf("[claude-cli] StartLogin: generated OAuth URL")
	return &LoginResult{URL: authURL}, nil
}

// SubmitCode exchanges the authorization code for tokens and writes credentials.
// code must be in the format "AUTHCODE#STATE" as shown on platform.claude.com.
func (c *Client) SubmitCode(code string) (bool, error) {
	as := getAuthState(c)
	as.mu.Lock()
	pkce := as.pkce
	as.mu.Unlock()

	if pkce == nil {
		return false, fmt.Errorf("no login flow in progress")
	}

	// Parse CODE#STATE.
	parts := strings.SplitN(code, "#", 2)
	if len(parts) != 2 {
		return false, fmt.Errorf("invalid code format: expected CODE#STATE")
	}
	authCode := parts[0]
	stateParam := parts[1]

	if stateParam != pkce.state {
		return false, fmt.Errorf("state mismatch: possible CSRF attack")
	}

	// Exchange code for tokens.
	tokens, err := c.exchangeCode(authCode, pkce.codeVerifier, pkce.state)
	if err != nil {
		return false, fmt.Errorf("token exchange: %w", err)
	}

	// Fetch subscription/rate-limit tier from the roles endpoint.
	roles, err := c.fetchRoles(tokens.AccessToken)
	if err != nil {
		log.Printf("[claude-cli] SubmitCode: could not fetch roles (non-fatal): %v", err)
	}

	// Write credentials to disk.
	if err := c.writeCredentials(tokens, roles); err != nil {
		return false, fmt.Errorf("save credentials: %w", err)
	}

	// Clear login state.
	as.mu.Lock()
	as.loginInProgress = false
	as.pkce = nil
	as.mu.Unlock()

	log.Printf("[claude-cli] SubmitCode: credentials saved successfully")
	return true, nil
}

// Logout clears stored Claude CLI credentials.
func (c *Client) Logout() error {
	cmd := exec.Command(c.binary, "auth", "logout")
	cmd.Env = c.env()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude auth logout: %w: %s", err, string(output))
	}
	return nil
}

// ── PKCE helpers ──────────────────────────────────────────────────────────────

func generateRandom(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func computeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func buildAuthURL(state, challenge string) string {
	params := url.Values{}
	params.Set("code", "true") // required by Claude's auth server
	params.Set("client_id", claudeClientID)
	params.Set("response_type", "code")
	params.Set("redirect_uri", claudeRedirectURI)
	params.Set("scope", claudeScopes)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("state", state)
	return claudeAuthURL + "?" + params.Encode()
}

// ── Token exchange ─────────────────────────────────────────────────────────────

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

func (c *Client) exchangeCode(authCode, verifier, state string) (*tokenResponse, error) {
	body, _ := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"code":          authCode,
		"redirect_uri":  claudeRedirectURI,
		"client_id":     claudeClientID,
		"code_verifier": verifier,
		"state":         state,
	})

	log.Printf("[claude-cli] exchangeCode: POST %s", claudeTokenURL)
	req, err := http.NewRequest(http.MethodPost, claudeTokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (%d): %s", resp.StatusCode, string(respBody))
	}

	var tokens tokenResponse
	if err := json.Unmarshal(respBody, &tokens); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	log.Printf("[claude-cli] exchangeCode: tokens received")
	return &tokens, nil
}

// ── Credentials storage ────────────────────────────────────────────────────────

// credentialsFile matches the format of ${CLAUDE_CONFIG_DIR}/.credentials.json.
type credentialsFile struct {
	ClaudeAiOauth *oauthTokens `json:"claudeAiOauth,omitempty"`
}

type oauthTokens struct {
	AccessToken      string   `json:"accessToken"`
	RefreshToken     string   `json:"refreshToken"`
	ExpiresAt        int64    `json:"expiresAt"`   // Unix milliseconds
	Scopes           []string `json:"scopes"`
	SubscriptionType *string  `json:"subscriptionType"`
	RateLimitTier    *string  `json:"rateLimitTier"`
}

// rolesResponse is the JSON response from the CLI roles endpoint.
type rolesResponse struct {
	SubscriptionType string `json:"subscription_type"`
	RateLimitTier    string `json:"rate_limit_tier"`
}

const claudeRolesURL = "https://api.anthropic.com/api/oauth/claude_cli/roles"

func (c *Client) fetchRoles(accessToken string) (*rolesResponse, error) {
	req, err := http.NewRequest(http.MethodGet, claudeRolesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("roles request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read roles response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("roles endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var roles rolesResponse
	if err := json.Unmarshal(body, &roles); err != nil {
		return nil, fmt.Errorf("parse roles response: %w", err)
	}
	log.Printf("[claude-cli] fetchRoles: subscriptionType=%s rateLimitTier=%s", roles.SubscriptionType, roles.RateLimitTier)
	return &roles, nil
}

// readCredentials loads and parses ${claudeDir}/.credentials.json.
// Returns (nil, nil) when the file doesn't exist.
func (c *Client) readCredentials() (*credentialsFile, error) {
	path := filepath.Join(c.claudeDir, ".credentials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var creds credentialsFile
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

// writeCredentialsAtomic writes oauth tokens via write-to-temp + rename so
// a crash mid-write can't leave a partial credentials file.
func (c *Client) writeCredentialsAtomic(oat *oauthTokens) error {
	creds := credentialsFile{ClaudeAiOauth: oat}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.claudeDir, 0755); err != nil {
		return err
	}
	path := filepath.Join(c.claudeDir, ".credentials.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// refreshTokens exchanges a refresh_token for a fresh access_token.
func (c *Client) refreshTokens(refreshToken string) (*tokenResponse, error) {
	body, _ := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     claudeClientID,
	})

	log.Printf("[claude-cli] refreshTokens: POST %s", claudeTokenURL)
	req, err := http.NewRequest(http.MethodPost, claudeTokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh failed (%d): %s", resp.StatusCode, string(respBody))
	}

	var tokens tokenResponse
	if err := json.Unmarshal(respBody, &tokens); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	log.Printf("[claude-cli] refreshTokens: new tokens received (expires_in=%d)", tokens.ExpiresIn)
	return &tokens, nil
}

func (c *Client) writeCredentials(tokens *tokenResponse, roles *rolesResponse) error {
	credPath := filepath.Join(c.claudeDir, ".credentials.json")

	scopes := strings.Fields(tokens.Scope)
	if len(scopes) == 0 {
		scopes = strings.Fields(claudeScopes)
	}

	expiresAt := time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second).UnixMilli()

	oat := &oauthTokens{
		AccessToken:      tokens.AccessToken,
		RefreshToken:     tokens.RefreshToken,
		ExpiresAt:        expiresAt,
		Scopes:           scopes,
		SubscriptionType: nil,
		RateLimitTier:    nil,
	}
	if roles != nil {
		if roles.SubscriptionType != "" {
			oat.SubscriptionType = &roles.SubscriptionType
		}
		if roles.RateLimitTier != "" {
			oat.RateLimitTier = &roles.RateLimitTier
		}
	}

	creds := credentialsFile{ClaudeAiOauth: oat}

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(c.claudeDir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(credPath, data, 0600); err != nil {
		return err
	}
	log.Printf("[claude-cli] writeCredentials: written to %s", credPath)
	return nil
}
