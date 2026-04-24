package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateNodeIDCreatesAndPersists(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "node_id")

	id1, err := LoadOrCreateNodeID(p)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(id1) != 32 {
		t.Fatalf("want 32-char hex id, got %d", len(id1))
	}

	id2, err := LoadOrCreateNodeID(p)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("must persist across calls: %s vs %s", id1, id2)
	}
}

func TestLoadOrCreateNodeIDRegeneratesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "node_id")
	if err := os.WriteFile(p, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := LoadOrCreateNodeID(p)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if len(id) != 32 {
		t.Fatalf("want 32-char id after regeneration, got %d", len(id))
	}
}
