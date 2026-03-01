package models

// RoleCapabilities maps roles to their granted capabilities.
var RoleCapabilities = map[string][]string{
	"admin": {
		"users:read",
		"users:write",
		"plugins:manage",
		"plugins:search",
		"system:admin",
	},
	"user": {
		"users:read:self",
		"plugins:search",
	},
}

// GetCapabilities returns the capability list for a given role.
func GetCapabilities(role string) []string {
	if caps, ok := RoleCapabilities[role]; ok {
		return caps
	}
	return []string{}
}
