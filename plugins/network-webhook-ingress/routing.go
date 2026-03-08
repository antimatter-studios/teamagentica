package main

import (
	"fmt"
	"strings"
)

// MatchRoute finds the best matching route for a given path.
// Returns nil if no route matches.
func MatchRoute(routeList []Route, path string) *Route {
	var matched *Route
	matchLen := 0
	for i, rt := range routeList {
		if strings.HasPrefix(path, rt.Prefix) && len(rt.Prefix) > matchLen {
			matched = &routeList[i]
			matchLen = len(rt.Prefix)
		}
	}
	return matched
}

// BuildRemainingPath strips the prefix from the path and ensures it starts with /.
func BuildRemainingPath(path, prefix string) string {
	remaining := strings.TrimPrefix(path, prefix)
	if remaining == "" {
		return "/"
	}
	if !strings.HasPrefix(remaining, "/") {
		remaining = "/" + remaining
	}
	return remaining
}

// BuildTargetURL constructs the full target URL for proxying.
func BuildTargetURL(host string, port int, remainingPath, rawQuery string) string {
	url := fmt.Sprintf("http://%s:%d%s", host, port, remainingPath)
	if rawQuery != "" {
		url += "?" + rawQuery
	}
	return url
}

// NormalizePrefix ensures a prefix starts with /.
func NormalizePrefix(prefix string) string {
	if !strings.HasPrefix(prefix, "/") {
		return "/" + prefix
	}
	return prefix
}
