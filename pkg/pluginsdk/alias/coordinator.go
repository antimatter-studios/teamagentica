package alias

import "strings"

// ParseCoordinatorResponse checks if the coordinator's response is a routing
// delegation. The expected format is:
//
//	ROUTE:@alias
//	message to forward
//
// Returns the alias name (without @), the forwarded message, and whether this
// is a delegation at all.
func ParseCoordinatorResponse(response string) (alias string, message string, isDelegation bool) {
	if !strings.HasPrefix(response, "ROUTE:@") {
		return "", "", false
	}

	newlineIdx := strings.Index(response, "\n")
	if newlineIdx < 0 {
		// ROUTE:@alias with no message body.
		alias = strings.TrimPrefix(response, "ROUTE:@")
		return alias, "", true
	}

	alias = strings.TrimPrefix(response[:newlineIdx], "ROUTE:@")
	message = strings.TrimSpace(response[newlineIdx+1:])
	return alias, message, true
}
