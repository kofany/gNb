package util

import (
	"regexp"
	"strings"
)

// Matcher is responsible for comparing IRC userhosts with masks.
type Matcher struct{}

// MatchUserHost checks if the full userhost matches the mask.
func (m *Matcher) MatchUserHost(mask, userHost string) bool {
	// Split mask and userHost into parts
	maskParts := strings.SplitN(mask, "!", 2)
	hostParts := strings.SplitN(userHost, "!", 2)
	if len(maskParts) != 2 || len(hostParts) != 2 {
		return false // Invalid format
	}

	maskIdentHost := strings.SplitN(maskParts[1], "@", 2)
	hostIdentHost := strings.SplitN(hostParts[1], "@", 2)
	if len(maskIdentHost) != 2 || len(hostIdentHost) != 2 {
		return false // Invalid format
	}

	maskIdent, maskHost := maskIdentHost[0], maskIdentHost[1]
	hostIdent, hostHost := hostIdentHost[0], hostIdentHost[1]

	// Check ident
	if !m.matchWildcard(maskIdent, hostIdent) {
		return false
	}

	// Check host/IP
	return m.MatchHost(maskHost, hostHost)
}

// MatchHost checks if the host matches the mask, including wildcards and IPv6 expansion.
func (m *Matcher) MatchHost(mask, host string) bool {
	// Special handling for IPv6
	if strings.Contains(host, ":") {
		host = ExpandIPv6(host)
	}

	return m.matchWildcard(mask, host)
}

// matchWildcard converts wildcard patterns to regex and matches the string.
func (m *Matcher) matchWildcard(pattern, str string) bool {
	// Escape regex special characters
	pattern = regexp.QuoteMeta(pattern)
	// Replace wildcards with regex equivalents
	pattern = strings.Replace(pattern, `\*`, ".*", -1)    // * matches any string
	pattern = strings.Replace(pattern, `\?`, ".", -1)     // ? matches any character
	pattern = strings.Replace(pattern, `\#`, "[0-9]", -1) // # matches any digit
	// Add start and end anchors
	pattern = "^" + pattern + "$"

	matched, _ := regexp.MatchString(pattern, str)
	return matched
}
