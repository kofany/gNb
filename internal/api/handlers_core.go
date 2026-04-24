package api

import (
	"context"
	"os"
	"time"
)

// handleAuthLogin responds to re-login requests after auth is already
// complete. The real token check happens in Session.handshake before
// dispatch; registering this method means duplicate logins are refused
// cleanly rather than falling through as unknown_method.
func handleAuthLogin(_ context.Context, _ *Session, _ *RequestMsg) (any, *HandlerError) {
	return nil, &HandlerError{Code: ErrForbidden, Message: "already authenticated"}
}

func handleNodeInfo(_ context.Context, s *Session, _ *RequestMsg) (any, *HandlerError) {
	srv := s.server
	var (
		numBots   int
		connected int
	)
	if srv.deps.BotManager != nil {
		bots := srv.deps.BotManager.GetBots()
		numBots = len(bots)
		for _, b := range bots {
			if b.IsConnected() {
				connected++
			}
		}
	}
	return map[string]any{
		"node_id":            srv.nodeID,
		"node_name":          srv.cfg.NodeName,
		"api_version":        "1.0",
		"version":            srv.deps.Version,
		"pid":                os.Getpid(),
		"uptime_seconds":     int64(time.Since(srv.startTime).Seconds()),
		"num_bots":           numBots,
		"num_connected_bots": connected,
		"started_at":         srv.startTime.UTC().Format(time.RFC3339),
	}, nil
}
