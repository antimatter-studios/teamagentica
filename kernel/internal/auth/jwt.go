package auth

import (
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/antimatter-studios/teamagentica/kernel/internal/models"
)

var jwtSecret []byte

// jwtTTL is the token lifetime. Defaults to 30 days; override with
// JWT_TTL_HOURS environment variable.
var jwtTTL = func() time.Duration {
	if v := os.Getenv("JWT_TTL_HOURS"); v != "" {
		if h, err := strconv.Atoi(v); err == nil && h > 0 {
			return time.Duration(h) * time.Hour
		}
	}
	return 30 * 24 * time.Hour // 30 days
}()

// InitJWT sets the signing key used for all token operations.
func InitJWT(secret string) {
	jwtSecret = []byte(secret)
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
func GenerateToken(user *models.User) (string, error) {
	caps := models.GetCapabilities(user.Role)

	claims := Claims{
		UserID:       user.ID,
		Email:        user.Email,
		Role:         user.Role,
		Capabilities: caps,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(jwtTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
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
