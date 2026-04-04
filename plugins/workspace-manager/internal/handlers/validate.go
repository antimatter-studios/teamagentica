package handlers

import "strings"

// isValidWorkspaceKey checks that a workspace key is safe for use as a directory name.
func isValidWorkspaceKey(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// slugify converts a human-readable name into a filesystem/subdomain-safe ASCII key.
// "The Claude Code Testing Env" → "the-claude-code-testing-env"
func slugify(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if len(s) > 128 {
		s = s[:128]
	}
	return s
}

// isValidWorkspaceID is an alias kept for disk name validation.
func isValidWorkspaceID(id string) bool {
	return isValidWorkspaceKey(id)
}
