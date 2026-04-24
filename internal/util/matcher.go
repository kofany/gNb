package util

import (
	"regexp"
	"strings"
	"sync"
)

// Matcher is responsible for comparing IRC userhosts with masks.
// A process-wide cache of compiled patterns backs every Matcher instance,
// so callers can keep constructing Matcher{} literals without paying the
// regex-compile cost on repeated PRIVMSG dispatch against the same owner list.
type Matcher struct{}

// matcherCache is the shared pattern cache. Value type is *regexp.Regexp;
// nil values cache "this pattern failed to compile" to avoid retry storms.
var matcherCache sync.Map

// MatchUserHost checks if the full userhost matches the mask.
func (m *Matcher) MatchUserHost(mask, userHost string) bool {
	maskParts := strings.SplitN(mask, "!", 2)
	hostParts := strings.SplitN(userHost, "!", 2)
	if len(maskParts) != 2 || len(hostParts) != 2 {
		return false
	}

	maskIdentHost := strings.SplitN(maskParts[1], "@", 2)
	hostIdentHost := strings.SplitN(hostParts[1], "@", 2)
	if len(maskIdentHost) != 2 || len(hostIdentHost) != 2 {
		return false
	}

	maskIdent, maskHost := maskIdentHost[0], maskIdentHost[1]
	hostIdent, hostHost := hostIdentHost[0], hostIdentHost[1]

	if !m.matchWildcard(maskIdent, hostIdent) {
		return false
	}
	return m.MatchHost(maskHost, hostHost)
}

// MatchHost checks if the host matches the mask, including wildcards and IPv6 expansion.
func (m *Matcher) MatchHost(mask, host string) bool {
	if strings.Contains(host, ":") {
		host = ExpandIPv6(host)
	}
	return m.matchWildcard(mask, host)
}

// matchWildcard converts the IRC wildcard pattern into a regex, caches the
// compilation, and reports whether str matches it. Invalid patterns
// (which should not occur in practice — they're static owner masks) are
// treated as no-match and also cached as a nil regex to avoid retry storms.
func (m *Matcher) matchWildcard(pattern, str string) bool {
	re := m.compile(pattern)
	if re == nil {
		return false
	}
	return re.MatchString(str)
}

func (m *Matcher) compile(pattern string) *regexp.Regexp {
	if cached, ok := matcherCache.Load(pattern); ok {
		if re, _ := cached.(*regexp.Regexp); re != nil {
			return re
		}
		return nil
	}

	// Escape regex special characters, then substitute IRC wildcards.
	rePat := regexp.QuoteMeta(pattern)
	rePat = strings.ReplaceAll(rePat, `\*`, ".*")
	rePat = strings.ReplaceAll(rePat, `\?`, ".")
	rePat = strings.ReplaceAll(rePat, `\#`, "[0-9]")
	rePat = "^" + rePat + "$"

	re, err := regexp.Compile(rePat)
	if err != nil {
		matcherCache.Store(pattern, (*regexp.Regexp)(nil))
		return nil
	}
	matcherCache.Store(pattern, re)
	return re
}
