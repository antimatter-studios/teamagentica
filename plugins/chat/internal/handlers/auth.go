package handlers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

func parseUserID(authHeader string) (uint, error) {
	token := strings.TrimPrefix(authHeader, "Bearer ")
	segments := strings.Split(token, ".")
	if len(segments) < 2 {
		return 0, fmt.Errorf("invalid token format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		return 0, fmt.Errorf("decoding payload: %w", err)
	}
	var claims struct {
		UserID uint `json:"user_id"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0, fmt.Errorf("parsing claims: %w", err)
	}
	if claims.UserID == 0 {
		return 0, fmt.Errorf("invalid user_id")
	}
	return claims.UserID, nil
}
