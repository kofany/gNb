package util

import (
	"strings"
	"testing"
)

func TestValidateGeneratedNick(t *testing.T) {
	for _, nick := range []string{"silentbull", "roughpanda", "quickrider", "bravedruid"} {
		if !isAlpha(nick) {
			t.Fatalf("expected alpha-only nick, got %q", nick)
		}
	}
}

func TestCombineNickParts(t *testing.T) {
	got, ok := combineNickParts("silent", "bull", 14)
	if !ok || got != "silentbull" {
		t.Fatalf("expected silentbull, got %q ok=%v", got, ok)
	}

	if _, ok := combineNickParts("thunder", "blacksmith", 10); ok {
		t.Fatal("expected oversized nick to fail")
	}
}

func TestNickCandidateValidation(t *testing.T) {
	good := []string{"silentbull", "roughcrab", "fuzzypanda", "bravescout"}
	for _, nick := range good {
		if !isNickCandidate(nick, 14) {
			t.Fatalf("expected %q to be accepted", nick)
		}
	}

	bad := []string{"aa", "abc123", "toolongnicknamehere", "zzzqqq"}
	for _, nick := range bad {
		if isNickCandidate(nick, 14) {
			t.Fatalf("expected %q to be rejected", nick)
		}
	}
}

func TestUniqueNickPool(t *testing.T) {
	leadWords = []string{"silent", "rough", "fuzzy", "brave", "quick"}
	tailWords = []string{"bull", "panda", "rider", "scout", "druid", "crab"}
	recentNickSet = make(map[string]struct{})
	recentNickQueue = nil

	got, err := generateNickPool(20, 14)
	if err != nil {
		t.Fatalf("generate nick pool: %v", err)
	}
	if len(got) != 20 {
		t.Fatalf("expected 20 nicks, got %d", len(got))
	}

	seen := make(map[string]struct{}, len(got))
	for _, nick := range got {
		if _, exists := seen[nick]; exists {
			t.Fatalf("duplicate nick in pool: %q", nick)
		}
		seen[nick] = struct{}{}
		if strings.Contains(nick, "-") || strings.Contains(nick, "_") {
			t.Fatalf("unexpected separator in nick: %q", nick)
		}
	}
}
