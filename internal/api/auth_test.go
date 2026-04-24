package api

import "testing"

func TestTokenCheckerMatches(t *testing.T) {
	c := newTokenChecker("supersecret")
	ok, rl := c.CheckToken("1.2.3.4", "supersecret")
	if !ok || rl {
		t.Fatalf("want ok=true, got ok=%v rl=%v", ok, rl)
	}
}

func TestTokenCheckerRejectsWrong(t *testing.T) {
	c := newTokenChecker("supersecret")
	ok, rl := c.CheckToken("1.2.3.4", "nope")
	if ok || rl {
		t.Fatalf("want ok=false, rl=false, got ok=%v rl=%v", ok, rl)
	}
}

func TestTokenCheckerRateLimits(t *testing.T) {
	c := newTokenChecker("x")
	for i := 0; i < 5; i++ {
		c.CheckToken("1.1.1.1", "bad")
	}
	_, rl := c.CheckToken("1.1.1.1", "bad")
	if !rl {
		t.Fatalf("want rate-limited after 5 failures")
	}
	_, rl2 := c.CheckToken("2.2.2.2", "bad")
	if rl2 {
		t.Fatalf("rate limit must be per-IP")
	}
}
