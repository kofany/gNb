// Plik internal/util/matcher.go

package util

import (
	"regexp"
	"strings"
)

// Matcher jest strukturą odpowiedzialną za porównywanie userhostów IRC z maskami.
type Matcher struct{}

// MatchUserHost sprawdza, czy pełny userhost (nick!ident@host) pasuje do maski.
func (m *Matcher) MatchUserHost(mask, userHost string) bool {
	// Rozdzielamy maskę i userHost na odpowiednie części
	maskParts := strings.SplitN(mask, "!", 2)
	hostParts := strings.SplitN(userHost, "!", 2)
	if len(maskParts) != 2 || len(hostParts) != 2 {
		return false // Nieprawidłowy format maski lub hosta
	}

	maskIdentHost := strings.SplitN(maskParts[1], "@", 2)
	hostIdentHost := strings.SplitN(hostParts[1], "@", 2)
	if len(maskIdentHost) != 2 || len(hostIdentHost) != 2 {
		return false // Nieprawidłowy format maski lub hosta
	}

	maskIdent, maskHost := maskIdentHost[0], maskIdentHost[1]
	hostIdent, hostHost := hostIdentHost[0], hostIdentHost[1]

	// Sprawdzenie identa
	if !m.matchWildcard(maskIdent, hostIdent) {
		return false
	}

	// Sprawdzenie hosta/IP
	return m.MatchHost(maskHost, hostHost)
}

// MatchHost sprawdza, czy host pasuje do maski, uwzględniając wildcardy i pełną formę IPv6.
func (m *Matcher) MatchHost(mask, host string) bool {
	// Specjalne traktowanie dla IPv6 - rozwijamy do pełnej formy, korzystając z funkcji ExpandIPv6 z iputil.go
	if strings.Contains(host, ":") {
		host = ExpandIPv6(host)
	}

	return m.matchWildcard(mask, host)
}

// matchWildcard zamienia maskę z wildcardami (*, ?, #) na regex i sprawdza zgodność z tekstem.
func (m *Matcher) matchWildcard(pattern, str string) bool {
	// Ucieczka specjalnych znaków regex
	pattern = regexp.QuoteMeta(pattern)
	// Zamiana wildcardów na regex
	pattern = strings.Replace(pattern, `\*`, ".*", -1)    // * zastępuje dowolny ciąg znaków
	pattern = strings.Replace(pattern, `\?`, ".", -1)     // ? zastępuje dowolny znak
	pattern = strings.Replace(pattern, `\#`, "[0-9]", -1) // # zastępuje jedną cyfrę
	// Dodanie anchorów początku i końca
	pattern = "^" + pattern + "$"

	matched, _ := regexp.MatchString(pattern, str)
	return matched
}
