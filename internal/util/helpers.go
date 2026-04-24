package util

import "strings"

// ContainsIgnoreCase reports whether slice contains item using a
// case-insensitive comparison.
func ContainsIgnoreCase(slice []string, item string) bool {
	itemLower := strings.ToLower(item)
	for _, s := range slice {
		if strings.ToLower(s) == itemLower {
			return true
		}
	}
	return false
}

// IsTargetNick reports whether nick is in targetNicks (case-insensitive).
func IsTargetNick(nick string, targetNicks []string) bool {
	return ContainsIgnoreCase(targetNicks, nick)
}
