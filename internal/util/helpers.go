package util

import "strings"

// Contains checks if a slice contains a specific string
func Contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
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
	if len(nick) == 1 && nick >= "a" && nick <= "z" {
		return true
	}
	return false
}
