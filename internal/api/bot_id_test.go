package api

import "testing"

func TestComputeBotIDDeterministic(t *testing.T) {
	a := ComputeBotID("irc.example", 6667, "1.2.3.4", 0)
	b := ComputeBotID("irc.example", 6667, "1.2.3.4", 0)
	if a != b {
		t.Fatalf("same input should produce same id: %s vs %s", a, b)
	}
	if len(a) != 12 {
		t.Fatalf("id must be 12 chars, got %d", len(a))
	}
}

func TestComputeBotIDDistinguishesDuplicates(t *testing.T) {
	a := ComputeBotID("irc.example", 6667, "1.2.3.4", 0)
	b := ComputeBotID("irc.example", 6667, "1.2.3.4", 1)
	if a == b {
		t.Fatalf("index must disambiguate duplicates: %s == %s", a, b)
	}
}

func TestComputeBotIDDistinguishesTuple(t *testing.T) {
	a := ComputeBotID("irc.a", 6667, "vh", 0)
	b := ComputeBotID("irc.b", 6667, "vh", 0)
	if a == b {
		t.Fatalf("different server must produce different id")
	}
}
