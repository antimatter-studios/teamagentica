package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"roboslop/kernel/internal/models"
)

var jwtSecret []byte

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
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

// GenerateServiceToken creates a signed JWT for a service account.
func GenerateServiceToken(name string, capabilities []string, expiresIn time.Duration) (string, error) {
	claims := Claims{
		UserID:       0,
		Email:        "service:" + name,
		Role:         "service",
		Capabilities: capabilities,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiresIn)),
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
