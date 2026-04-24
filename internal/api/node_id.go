package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const nodeIDFile = "data/node_id"

var nodeIDMu sync.Mutex

// LoadOrCreateNodeID returns the persistent node UUID, creating the file on first call.
// path is the file location (tests pass a temp dir; production uses data/node_id).
func LoadOrCreateNodeID(path string) (string, error) {
	nodeIDMu.Lock()
	defer nodeIDMu.Unlock()

	if raw, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(raw))
		if len(id) == 32 {
			return id, nil
		}
	}
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate node id: %w", err)
	}
	id := hex.EncodeToString(buf[:])
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("mkdir for node id: %w", err)
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write node id: %w", err)
	}
	return id, nil
}

// DefaultNodeIDPath is the canonical location under the working directory.
func DefaultNodeIDPath() string {
	return nodeIDFile
}
