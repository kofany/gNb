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

// NickEqualRFC compares two IRC nicks using RFC 2812 §2.2 case mapping:
// `[]\~` and `{}|^` are equivalent (the "scandinavian" rule), in addition
// to standard ASCII case folding. Matches the algorithm go-ircevo uses
// internally to recognise its own NICK events.
func NickEqualRFC(a, b string) bool {
	return canonicalizeNick(a) == canonicalizeNick(b)
}

func canonicalizeNick(nick string) string {
	out := make([]byte, len(nick))
	for i := 0; i < len(nick); i++ {
		ch := nick[i]
		switch ch {
		case '[':
			ch = '{'
		case ']':
			ch = '}'
		case '\\':
			ch = '|'
		case '~':
			ch = '^'
		}
		if ch >= 'A' && ch <= 'Z' {
			ch += 'a' - 'A'
		}
		out[i] = ch
	}
	return string(out)
}
