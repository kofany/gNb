package util

import "strings"

// Contains checks if a slice contains a specific string
func Contains(slice []string, item string) bool {
	itemLower := strings.ToLower(item)
	for _, s := range slice {
		if strings.ToLower(s) == itemLower {
			return true
		}
	}
	return false
}

// IsTargetNick checks if a nick is a target nick to catch
func IsTargetNick(nick string, targetNicks []string) bool {
	nick = strings.ToLower(nick)
	for _, target := range targetNicks {
		if strings.ToLower(target) == nick {
			return true
		}
	}
	return false
}

func ContainsIgnoreCase(slice []string, item string) bool {
	itemLower := strings.ToLower(item)
	for _, s := range slice {
		if strings.ToLower(s) == itemLower {
			return true
		}
	}
	return false
}
