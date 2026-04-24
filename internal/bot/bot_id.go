package bot

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
)

// computeBotID derives a stable 12-char hex identifier for a bot from its
// IRC connection tuple and its index within cfg.Bots. Kept in sync with
// internal/api.ComputeBotID — both use identical hashing so the panel can
// address bots by the same id the bot uses to tag events.
func computeBotID(server string, port int, vhost string, index int) string {
	sum := sha1.Sum(fmt.Appendf(nil, "%s:%d:%s:%d", server, port, vhost, index))
	return hex.EncodeToString(sum[:])[:12]
}
