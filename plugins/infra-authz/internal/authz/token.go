package authz

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TokenClaims are the JWT claims for an authz token.
type TokenClaims struct {
	jwt.RegisteredClaims
	Principal string   `json:"principal"`
	ProjectID string   `json:"project_id"`
	AgentType string   `json:"agent_type,omitempty"`
	SessionID string   `json:"session_id,omitempty"`
	Scopes    []string `json:"scopes"`
}

// TokenService handles JWT minting and verification with Ed25519.
type TokenService struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	keyID      string
}

// NewTokenService loads or generates an Ed25519 keypair from the data directory.
func NewTokenService(dataPath string) (*TokenService, error) {
	privPath := filepath.Join(dataPath, "authz_ed25519.pem")
	pubPath := filepath.Join(dataPath, "authz_ed25519_pub.pem")

	var privKey ed25519.PrivateKey
	var pubKey ed25519.PublicKey

	privPEM, err := os.ReadFile(privPath)
	if err != nil {
		// Generate new keypair
		pubKey, privKey, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate keypair: %w", err)
		}
		// Save private key
		privBlock := &pem.Block{Type: "PRIVATE KEY", Bytes: privKey.Seed()}
		if err := os.WriteFile(privPath, pem.EncodeToMemory(privBlock), 0600); err != nil {
			return nil, fmt.Errorf("write private key: %w", err)
		}
		// Save public key
		pubBlock := &pem.Block{Type: "PUBLIC KEY", Bytes: []byte(pubKey)}
		if err := os.WriteFile(pubPath, pem.EncodeToMemory(pubBlock), 0644); err != nil {
			return nil, fmt.Errorf("write public key: %w", err)
		}
	} else {
		// Load existing keypair
		block, _ := pem.Decode(privPEM)
		if block == nil {
			return nil, fmt.Errorf("failed to decode private key PEM")
		}
		seed := block.Bytes
		privKey = ed25519.NewKeyFromSeed(seed)
		pubKey = privKey.Public().(ed25519.PublicKey)
	}

	// Key ID is a truncated hash of the public key
	hash := sha256.Sum256([]byte(pubKey))
	keyID := base64.RawURLEncoding.EncodeToString(hash[:8])

	return &TokenService{
		privateKey: privKey,
		publicKey:  pubKey,
		keyID:      keyID,
	}, nil
}

// MintToken creates a signed JWT for the given principal.
func (ts *TokenService) MintToken(principal, projectID, agentType, sessionID string, scopes []string, expiryMinutes int) (string, error) {
	now := time.Now()
	if expiryMinutes <= 0 {
		expiryMinutes = 60
	}

	claims := TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.New().String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(expiryMinutes) * time.Minute)),
			Issuer:    "infra-authz",
		},
		Principal: principal,
		ProjectID: projectID,
		AgentType: agentType,
		SessionID: sessionID,
		Scopes:    scopes,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = ts.keyID

	return token.SignedString(ts.privateKey)
}

// VerifyToken validates a JWT and returns the claims.
func (ts *TokenService) VerifyToken(tokenStr string) (*TokenClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &TokenClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return ts.publicKey, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*TokenClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}

// JWKS returns a JSON Web Key Set containing the public key.
func (ts *TokenService) JWKS() map[string]interface{} {
	return map[string]interface{}{
		"keys": []map[string]interface{}{
			{
				"kty": "OKP",
				"crv": "Ed25519",
				"use": "sig",
				"kid": ts.keyID,
				"x":   base64.RawURLEncoding.EncodeToString(ts.publicKey),
			},
		},
	}
}

