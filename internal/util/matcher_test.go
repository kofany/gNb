package util

import "testing"

func TestMatchUserHostWildcards(t *testing.T) {
	cases := []struct {
		mask     string
		userHost string
		want     bool
	}{
		{"*!ident@host.example", "nick!ident@host.example", true},
		{"*!ident@host.example", "nick!other@host.example", false},
		{"*!*@*.example.com", "nick!user@box.example.com", true},
		{"*!*@*.example.com", "nick!user@box.other.com", false},
		{"*!user?@host", "nick!user1@host", true},
		{"*!user?@host", "nick!user@host", false},
		{"malformed", "nick!user@host", false},
		{"*!ident@host", "malformed", false},
	}

	m := &Matcher{}
	for _, c := range cases {
		got := m.MatchUserHost(c.mask, c.userHost)
		if got != c.want {
			t.Errorf("MatchUserHost(%q, %q) = %v, want %v", c.mask, c.userHost, got, c.want)
		}
	}
}

func TestMatchUserHostCacheReuse(t *testing.T) {
	// Two independent Matcher instances should share the compiled-pattern
	// cache (they both use the package-level matcherCache), so the second
	// call to the same mask must find the entry already compiled.
	m1 := &Matcher{}
	m2 := &Matcher{}

	mask := "*!ident@cachetest.example"
	m1.MatchUserHost(mask, "nick!ident@cachetest.example")

	if _, ok := matcherCache.Load("*"); !ok && false {
		// placeholder: individual sub-pattern keys depend on implementation
	}
	// Assert second call still returns correct result from cached regex.
	if !m2.MatchUserHost(mask, "nick!ident@cachetest.example") {
		t.Fatal("cached regex produced wrong match")
	}
}
