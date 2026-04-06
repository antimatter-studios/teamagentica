package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/antimatter-studios/teamagentica/plugins/system-user-manager/internal/storage"
)

var jwtSecret []byte
var tokenExpiry = 24 * time.Hour
var refreshTokenExpiry = 30 * 24 * time.Hour

// InitJWT sets the signing key and optional expiry for all token operations.
func InitJWT(secret string, expiryHours int) {
	jwtSecret = []byte(secret)
	if expiryHours > 0 {
		tokenExpiry = time.Duration(expiryHours) * time.Hour
	}
}

// Claims holds the JWT payload.
type Claims struct {
	UserID       uint     `json:"user_id"`
	Email        string   `json:"email"`
	Role         string   `json:"role"`
	Capabilities []string `json:"capabilities"`
	jwt.RegisteredClaims
}

// GenerateToken creates a signed JWT for the given user.
func GenerateToken(user *storage.User) (string, error) {
	caps := storage.GetCapabilities(user.Role)

	claims := Claims{
		UserID:       user.ID,
		Email:        user.Email,
		Role:         user.Role,
		Capabilities: caps,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(tokenExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

// GenerateRefreshToken creates a random opaque refresh token and returns
// the raw token (for the client) and its SHA-256 hash (for DB storage).
func GenerateRefreshToken() (raw string, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = hex.EncodeToString(b)
	h := sha256.Sum256([]byte(raw))
	hash = fmt.Sprintf("%x", h)
	return raw, hash, nil
}

// RefreshTokenExpiry returns the configured refresh token lifetime.
func RefreshTokenExpiry() time.Duration {
	return refreshTokenExpiry
}

// ValidateToken parses and validates the token string, returning the claims.
func ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}
