package api

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
)

// ComputeBotID derives a stable 12-char hex identifier for a bot from its
// IRC connection tuple and its index within cfg.Bots. The index disambiguates
// multiple bots that share {server, port, vhost}.
func ComputeBotID(server string, port int, vhost string, index int) string {
	sum := sha1.Sum(fmt.Appendf(nil, "%s:%d:%s:%d", server, port, vhost, index))
	return hex.EncodeToString(sum[:])[:12]
}
